package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"

	"mc/verbs"
)

func decodeConfigRequest(stdin io.Reader, target any) error {
	body, err := io.ReadAll(io.LimitReader(stdin, 64*1024+1))
	if err != nil || len(body) > 64*1024 {
		return verbs.Usagef("The configuration request exceeds the size limit.")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return verbs.Usagef("The configuration request is invalid.")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return verbs.Usagef("The configuration request contains extra data.")
	}
	return nil
}

func commandConfig(args []string, stdin io.Reader) (any, error) {
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(identity, "configuration"); err != nil {
		return nil, err
	}
	if len(args) != 1 {
		return nil, verbs.Usagef("A configuration action is required.")
	}
	switch args[0] {
	case "show":
		return verbs.ConfigShow()
	case "save":
		var request verbs.ConfigSaveRequest
		if err := decodeConfigRequest(stdin, &request); err != nil {
			return nil, err
		}
		return verbs.ConfigSave(request, func(values map[string]int) error {
			body, err := json.Marshal(values)
			if err != nil {
				return err
			}
			var output, diagnostic bytes.Buffer
			if run([]string{"__config-settings"}, bytes.NewReader(body), &output, &diagnostic) != 0 {
				return verbs.Domainf("The deployment could not apply the settings.")
			}
			return nil
		})
	default:
		return nil, verbs.Usagef("The configuration action is unsupported.")
	}
}

func commandConfigSettings(args []string, stdin io.Reader) (any, error) {
	identity, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(identity, "configuration"); err != nil {
		return nil, err
	}
	if len(args) != 0 {
		return nil, verbs.Usagef("The settings projection takes no arguments.")
	}
	values := map[string]int{}
	if err := decodeConfigRequest(stdin, &values); err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) { return verbs.ConfigApplySettings(db, values) })
}
