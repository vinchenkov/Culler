package wiki

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"mc/repository"
)

type Node struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Body        string   `json:"body"`
	Size        int      `json:"size"`
	Links       []string `json:"links"`
	Backlinks   []string `json:"backlinks"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type Graph struct {
	Root        string   `json:"root"`
	GeneratedAt string   `json:"generatedAt"`
	Types       []string `json:"types"`
	Nodes       []Node   `json:"nodes"`
	Edges       []Edge   `json:"edges"`
}

type View struct {
	Project        string `json:"project"`
	AcceptedCommit string `json:"accepted_commit"`
	Status         string `json:"status"`
	Graph          Graph  `json:"graph"`
}

var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
var firstHeading = regexp.MustCompile(`(?m)^#\s+(.+)$`)

// Read uses the accepted branch and never reads the operator's checkout files.
func Read(ctx context.Context, root, project string) (View, error) {
	view := View{Project: project, Status: "absent", Graph: Graph{Root: "openwiki", Types: []string{}, Nodes: []Node{}, Edges: []Edge{}}}
	git := repository.Git{Root: root}
	commit, err := git.Branch(ctx, "main")
	if err != nil {
		return view, err
	}
	view.AcceptedCommit = commit
	files, err := readFiles(ctx, git, commit)
	if err != nil {
		return view, err
	}
	if err := includeLinkedDocuments(ctx, git, commit, files); err != nil {
		return view, err
	}
	var names []string
	for name := range files {
		rel := strings.TrimPrefix(name, "openwiki/")
		if !strings.HasSuffix(rel, ".md") || path.Base(rel) == "INSTRUCTIONS.md" || path.Base(rel) == "log.md" {
			continue
		}
		hidden := false
		for _, component := range strings.Split(rel, "/") {
			if strings.HasPrefix(component, ".") {
				hidden = true
			}
		}
		if !hidden {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	types := map[string]bool{}
	for _, name := range names {
		if !utf8.Valid(files[name]) {
			return view, fmt.Errorf("the wiki document requires UTF-8 text: %s", name)
		}
		id := documentID(name)
		meta, tags, body := frontmatter(string(files[name]))
		title := meta["title"]
		kind := meta["type"]
		if title == "" && path.Base(id) == "index" {
			title = path.Base(path.Dir(id))
			if title == "." {
				title = "Home"
			}
		}
		if title == "" {
			if match := firstHeading.FindStringSubmatch(body); match != nil {
				title = strings.TrimSpace(match[1])
			}
		}
		if title == "" {
			title = path.Base(id)
		}
		if kind == "" {
			kind = "Reference"
			if path.Base(id) == "index" {
				kind = "Section"
			}
		}
		types[kind] = true
		view.Graph.Nodes = append(view.Graph.Nodes, Node{ID: id, Title: title, Type: kind, Description: meta["description"], Tags: tags, Body: body, Size: len(body), Links: []string{}, Backlinks: []string{}})
	}
	byID := map[string]int{}
	for i, node := range view.Graph.Nodes {
		byID[node.ID] = i
	}
	seen := map[string]bool{}
	for i := range view.Graph.Nodes {
		node := &view.Graph.Nodes[i]
		for _, match := range markdownLink.FindAllStringSubmatch(node.Body, -1) {
			u, err := url.Parse(match[1])
			if err != nil || u.IsAbs() || u.Host != "" {
				continue
			}
			rel := u.Path
			if strings.HasSuffix(rel, "/") {
				rel += "index.md"
			}
			if !strings.HasSuffix(rel, ".md") {
				continue
			}
			target := strings.TrimSuffix(path.Join(path.Dir(node.ID), rel), ".md")
			if strings.HasPrefix(rel, "/") {
				target = documentID(strings.TrimPrefix(path.Clean(rel), "/"))
			}
			j, ok := byID[target]
			key := node.ID + "\x00" + target
			if !ok || target == node.ID || seen[key] {
				continue
			}
			seen[key] = true
			node.Links = append(node.Links, target)
			view.Graph.Nodes[j].Backlinks = append(view.Graph.Nodes[j].Backlinks, node.ID)
			view.Graph.Edges = append(view.Graph.Edges, Edge{Source: node.ID, Target: target})
		}
	}
	for kind := range types {
		view.Graph.Types = append(view.Graph.Types, kind)
	}
	sort.Strings(view.Graph.Types)
	if len(names) > 0 {
		view.Status = "ready"
	}
	return view, nil
}

// The visualizer consumes scalar display fields and simple tag lists.
func frontmatter(raw string) (map[string]string, []string, string) {
	meta, tags := map[string]string{}, []string{}
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return meta, tags, raw
	}
	lines := strings.SplitAfter(raw, "\n")
	end, offset := -1, len(lines[0])
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			offset += len(lines[i])
			break
		}
		offset += len(lines[i])
	}
	if end < 0 {
		return meta, tags, raw
	}
	list := false
	for _, line := range lines[1:end] {
		trimmed := strings.TrimSpace(line)
		if list && strings.HasPrefix(trimmed, "- ") {
			tags = append(tags, unquote(strings.TrimPrefix(trimmed, "- ")))
			continue
		}
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		list = key == "tags" && value == ""
		if key == "tags" && strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			for _, tag := range strings.Split(value[1:len(value)-1], ",") {
				if tag = unquote(tag); tag != "" {
					tags = append(tags, tag)
				}
			}
		} else {
			meta[key] = unquote(value)
		}
	}
	return meta, tags, raw[offset:]
}

func unquote(text string) string {
	text = strings.TrimSpace(text)
	if len(text) >= 2 && ((text[0] == '"' && text[len(text)-1] == '"') || (text[0] == '\'' && text[len(text)-1] == '\'')) {
		return text[1 : len(text)-1]
	}
	return text
}

func documentID(name string) string {
	if strings.HasPrefix(name, "openwiki/") {
		return strings.TrimSuffix(strings.TrimPrefix(name, "openwiki/"), ".md")
	}
	return "../" + strings.TrimSuffix(name, ".md")
}

func includeLinkedDocuments(ctx context.Context, git repository.Git, commit string, files map[string][]byte) error {
	var queue []string
	var total int
	for name, body := range files {
		queue = append(queue, name)
		total += len(body)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		for _, match := range markdownLink.FindAllStringSubmatch(string(files[name]), -1) {
			u, err := url.Parse(match[1])
			if err != nil || u.IsAbs() || u.Host != "" {
				continue
			}
			target := path.Join(path.Dir(name), u.Path)
			if strings.HasPrefix(u.Path, "/") {
				target = strings.TrimPrefix(path.Clean(u.Path), "/")
			}
			if !strings.HasSuffix(target, ".md") || target == ".." || strings.HasPrefix(target, "../") {
				continue
			}
			if _, exists := files[target]; exists {
				continue
			}
			hidden := false
			for _, part := range strings.Split(target, "/") {
				hidden = hidden || strings.HasPrefix(part, ".")
			}
			if hidden {
				continue
			}
			entries, err := git.Tree(ctx, commit, target)
			if err != nil {
				return err
			}
			if len(entries) != 1 || entries[0].Path != target {
				continue
			}
			if entries[0].Size > int64(maxWikiBytes-total) {
				return fmt.Errorf("the project documents exceed the 64 MiB read limit")
			}
			body, err := git.ReadTreeFile(ctx, entries[0], maxWikiBytes)
			if err != nil {
				return err
			}
			files[target] = body
			total += len(body)
			queue = append(queue, target)
		}
	}
	return nil
}
