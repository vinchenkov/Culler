package opencodecatalog

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// LiveModelListAddress is the address of the live model list. Only a release changes it.
const LiveModelListAddress = "https://models.opencode.ai/experimental/v2.0/api.json"

// MaxModelRecordBytes limits the model record of one live model.
const MaxModelRecordBytes = 24 << 10

//go:embed facts/server.mjs facts/package.json
var factsPlugin embed.FS

// FactsPlugin returns the files of the OpenCode plugin that reports the model facts.
// The release catalog generator uses the same files.
func FactsPlugin() map[string][]byte {
	files := map[string][]byte{}
	for _, name := range []string{"server.mjs", "package.json"} {
		data, err := factsPlugin.ReadFile("facts/" + name)
		if err != nil {
			panic(err)
		}
		files[name] = data
	}
	return files
}

// Digest identifies the release catalog.
var Digest = sync.OnceValue(func() string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
})

// ModelFact is one model as the pinned OpenCode derives it from a model record.
type ModelFact struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	Tools       bool     `json:"tools"`
	Context     int      `json:"context"`
	Output      int      `json:"output"`
	InputPrice  float64  `json:"input_price"`
	OutputPrice float64  `json:"output_price"`
	Address     string   `json:"address"`
	Package     string   `json:"package"`
	Variants    []string `json:"variants"`
	// Sources names the record entries that produce the model, in record order.
	Sources []string `json:"sources"`
	// Digest is equal for two models that OpenCode runs in the same way.
	Digest string `json:"digest"`
}

// ProviderFacts holds the models that the pinned OpenCode derives for one provider.
type ProviderFacts struct {
	ID     string
	Models []ModelFact
}

// ParseFacts reads the report of the facts plugin. It omits each model with an invalid shape.
func ParseFacts(data []byte) ([]ProviderFacts, error) {
	var report []struct {
		ID     string            `json:"id"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(data, &report); err != nil || len(report) == 0 {
		return nil, fmt.Errorf("The model facts of OpenCode are invalid.")
	}
	out := make([]ProviderFacts, 0, len(report))
	for _, provider := range report {
		facts := ProviderFacts{ID: provider.ID}
		for _, raw := range provider.Models {
			var fact ModelFact
			if json.Unmarshal(raw, &fact) == nil && validSHA256(fact.Digest) {
				facts.Models = append(facts.Models, fact)
			}
		}
		out = append(out, facts)
	}
	return out, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

// LiveRecord holds the entries of the live model list for the providers of the release catalog.
type LiveRecord struct {
	// Body is the model record that the pinned OpenCode derives the live models from.
	Body    []byte
	entries map[string]map[string]json.RawMessage
}

// ReadLiveRecord keeps the model entries of each release catalog provider.
// The release catalog supplies the name, the website, and the key variable of each provider.
func ReadLiveRecord(data []byte) (LiveRecord, error) {
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(data, &providers); err != nil {
		return LiveRecord{}, fmt.Errorf("The live model list is invalid.")
	}
	record := LiveRecord{entries: map[string]map[string]json.RawMessage{}}
	var out bytes.Buffer
	out.WriteByte('{')
	for _, provider := range Release().Providers {
		var source struct {
			Models json.RawMessage `json:"models"`
		}
		var entries map[string]json.RawMessage
		if json.Unmarshal(providers[provider.ID], &source) != nil || json.Unmarshal(source.Models, &entries) != nil || len(entries) == 0 {
			continue
		}
		var models bytes.Buffer
		if json.Compact(&models, source.Models) != nil {
			continue
		}
		if len(record.entries) != 0 {
			out.WriteByte(',')
		}
		writeRecordProvider(&out, provider, models.Bytes())
		record.entries[provider.ID] = entries
	}
	out.WriteByte('}')
	if len(record.entries) == 0 {
		return LiveRecord{}, fmt.Errorf("The live model list holds no provider of the release catalog.")
	}
	record.Body = out.Bytes()
	return record, nil
}

func writeRecordProvider(out *bytes.Buffer, provider Provider, models []byte) {
	key, _ := json.Marshal(provider.ID)
	head, _ := json.Marshal(struct {
		ID   string   `json:"id"`
		Name string   `json:"name"`
		Env  []string `json:"env"`
		Doc  string   `json:"doc"`
	}{provider.ID, provider.Name, []string{provider.KeyVariable}, provider.Website})
	out.Write(key)
	out.WriteByte(':')
	out.Write(head[:len(head)-1])
	out.WriteString(`,"models":`)
	out.Write(models)
	out.WriteByte('}')
}

// modelRecord returns a model record that holds only the entries of one model.
func (record LiveRecord) modelRecord(provider Provider, sources []string) (string, bool) {
	if len(sources) == 0 || len(sources) > 2 || (len(sources) == 2 && sources[0] == sources[1]) {
		return "", false
	}
	var models bytes.Buffer
	models.WriteByte('{')
	for index, source := range sources {
		raw, ok := record.entries[provider.ID][source]
		if !ok {
			return "", false
		}
		if index != 0 {
			models.WriteByte(',')
		}
		key, _ := json.Marshal(source)
		models.Write(key)
		models.WriteByte(':')
		if json.Compact(&models, raw) != nil {
			return "", false
		}
	}
	models.WriteByte('}')
	var out bytes.Buffer
	out.WriteByte('{')
	writeRecordProvider(&out, provider, models.Bytes())
	out.WriteByte('}')
	return out.String(), out.Len() <= MaxModelRecordBytes
}

type recordedRequest struct {
	KeyPlacement  string `json:"key_placement"`
	ModelLocation string `json:"model_location"`
}

var recordedPackages = sync.OnceValue(func() map[string]recordedRequest {
	packages := map[string]recordedRequest{}
	if err := json.Unmarshal(Release().Requests, &packages); err != nil {
		panic(err)
	}
	return packages
})

// RecordedPackage returns the key placement and the model location of a recorded package.
func RecordedPackage(name string) (keyPlacement, modelLocation string, ok bool) {
	request, ok := recordedPackages()[name]
	ok = ok && slices.Contains([]string{"bearer", "x-api-key", "x-goog-api-key"}, request.KeyPlacement) &&
		slices.Contains([]string{"body", "path"}, request.ModelLocation)
	return request.KeyPlacement, request.ModelLocation, ok
}

var (
	addressHost    = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	addressPort    = regexp.MustCompile(`^[1-9][0-9]{0,4}$`)
	addressSegment = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

// addressParts returns the origin and the path of a fixed HTTPS address.
// The limited character set gives each URL parser the same origin and the same path.
func addressParts(address string) (origin, path string, ok bool) {
	for _, r := range address {
		if r <= 0x20 || r >= 0x7f || strings.ContainsRune(`{}$<>\@?#%`, r) {
			return "", "", false
		}
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.User != nil {
		return "", "", false
	}
	host, port := strings.ToLower(parsed.Hostname()), parsed.Port()
	if !addressHost.MatchString(host) || strings.Contains(host, "..") || (port != "" && !addressPort.MatchString(port)) {
		return "", "", false
	}
	if port != "" && port != "443" {
		host += ":" + port
	}
	segments := strings.Split(strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), "/"), "/")
	for _, segment := range segments {
		if parsed.Path != "" && parsed.Path != "/" && (!addressSegment.MatchString(segment) || segment == "." || segment == "..") {
			return "", "", false
		}
	}
	return host, parsed.Path, true
}

// liveAddress returns the address of a live model below a release catalog origin of the provider.
func liveAddress(provider Provider, address string) (string, bool) {
	origin, path, ok := addressParts(address)
	if !ok {
		return "", false
	}
	for _, model := range provider.Models {
		if released, _, ok := addressParts(model.Address); ok && released == origin {
			// The release catalog supplies the text of the origin.
			parsed, _ := url.Parse(model.Address)
			return "https://" + parsed.Host + path, true
		}
	}
	return "", false
}

func validText(value string, limit int) bool {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidLiveModel reports whether a model meets the live model conditions of a release catalog provider.
func ValidLiveModel(provider Provider, model Model) bool {
	address, ok := liveAddress(provider, model.Address)
	placement, location, recorded := RecordedPackage(model.Package)
	if !ok || address != model.Address || !recorded || placement != model.KeyPlacement || location != model.ModelLocation {
		return false
	}
	if !validText(model.ID, 256) || !validText(model.Name, 256) || model.Context < 0 || model.InputPrice < 0 || model.OutputPrice < 0 ||
		model.Variants == nil || len(model.Variants) > 64 || model.Record == "" || len(model.Record) > MaxModelRecordBytes || !json.Valid([]byte(model.Record)) {
		return false
	}
	for _, variant := range model.Variants {
		if !validText(variant, 64) {
			return false
		}
	}
	return true
}

// liveModel returns the live model of a model fact. It refuses a fact that fails a live model condition.
func liveModel(provider Provider, record LiveRecord, fact ModelFact) (Model, bool) {
	if fact.Type != "chat" || !fact.Tools || fact.Status == "deprecated" || fact.Output < 0 {
		return Model{}, false
	}
	address, ok := liveAddress(provider, fact.Address)
	if !ok {
		return Model{}, false
	}
	placement, location, _ := RecordedPackage(fact.Package)
	text, ok := record.modelRecord(provider, fact.Sources)
	if !ok {
		return Model{}, false
	}
	model := Model{ID: fact.ID, Name: fact.Name, Context: fact.Context, InputPrice: fact.InputPrice, OutputPrice: fact.OutputPrice,
		Address: address, Package: fact.Package, KeyPlacement: placement, ModelLocation: location, Variants: fact.Variants, Record: text}
	if !validText(model.Name, 256) {
		model.Name = model.ID
	}
	if model.Variants == nil {
		model.Variants = []string{}
	}
	return model, ValidLiveModel(provider, model)
}

// LiveModels returns the live models of each release catalog provider.
// The baseline holds the facts of the model record in the pinned OpenCode program.
// A model with the same digest in the baseline needs no model record, so it stays a release catalog model.
func LiveModels(record LiveRecord, baseline, live []ProviderFacts) map[string][]Model {
	known := map[string]map[string]string{}
	for _, provider := range baseline {
		known[provider.ID] = map[string]string{}
		for _, fact := range provider.Models {
			known[provider.ID][fact.ID] = fact.Digest
		}
	}
	out := map[string][]Model{}
	for _, facts := range live {
		provider, ok := Find(facts.ID)
		if !ok || out[facts.ID] != nil {
			continue
		}
		seen := map[string]bool{}
		for _, fact := range facts.Models {
			if seen[fact.ID] || known[facts.ID][fact.ID] == fact.Digest {
				continue
			}
			if model, ok := liveModel(provider, record, fact); ok {
				seen[fact.ID] = true
				out[facts.ID] = append(out[facts.ID], model)
			}
		}
	}
	return out
}

// WithLive returns the provider with its live models. New live models come first.
// A live model replaces the release catalog model with the same identifier.
func (provider Provider) WithLive(live []Model) Provider {
	if len(live) == 0 {
		return provider
	}
	replacement := map[string]Model{}
	for _, model := range live {
		replacement[model.ID] = model
	}
	released := map[string]bool{}
	for _, model := range provider.Models {
		released[model.ID] = true
	}
	models := make([]Model, 0, len(provider.Models)+len(live))
	for _, model := range live {
		if !released[model.ID] {
			models = append(models, model)
		}
	}
	for _, model := range provider.Models {
		if current, ok := replacement[model.ID]; ok {
			model = current
		}
		models = append(models, model)
	}
	provider.Models = models
	return provider
}
