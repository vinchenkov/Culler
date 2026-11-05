package verbs

import (
	"context"
	"encoding/json"
	"fmt"
	"mc/dispatch"
	"mc/repository"
)

// EvalContract uses the selected product revision's private contracts.
// The evaluator compiles this file only in a temporary module copy.
func EvalContract(data []byte) (any, error) {
	var request struct {
		Operation string `json:"operation"`
		RunID     string `json:"run_id"`
		Directive string `json:"directive"`
		Parts     []struct {
			Heading     string `json:"heading"`
			Explanation string `json:"explanation"`
			Facts       []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			} `json:"facts"`
			TextTag string `json:"text_tag"`
			Text    string `json:"text"`
			Rows    any    `json:"rows"`
		} `json:"parts"`
		ProjectSkills []RoleSkill `json:"project_skills"`
		Thesis        string      `json:"thesis"`
		Path          string      `json:"path"`
		Planes        []string    `json:"planes"`
		Repository    string      `json:"repository"`
		StartCommit   string      `json:"start_commit"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	switch request.Operation {
	case "input":
		all, err := RoleSkillSet(dispatch.RolePackager)
		if err != nil {
			return nil, err
		}
		var skills []RoleSkill
		for _, skill := range all {
			if skill.Name != "writing" {
				skills = append(skills, skill)
			}
		}
		for _, skill := range request.ProjectSkills {
			if skill.Name == "writing" {
				return nil, fmt.Errorf("the writing skill must reside in the editable directive")
			}
			skills = append(skills, skill)
		}
		pointer := skillPointer(skills)
		brief := runBrief{Role: dispatch.RolePackager, RunID: request.RunID, Directive: request.Directive + "\n\n" + pointer}
		var omitted, omittedFacts []string
		for _, part := range request.Parts {
			switch part.Heading {
			case "Verdict history", "Scribe report", "Earlier requirements":
				omitted = append(omitted, part.Heading)
				continue
			}
			p := briefPart{Heading: part.Heading, Explanation: part.Explanation, TextTag: part.TextTag, Text: part.Text, Rows: part.Rows}
			for _, fact := range part.Facts {
				if part.Heading == partGitState && storedScribeFact(fact.Label) {
					omittedFacts = append(omittedFacts, fact.Label)
					continue
				}
				p.Facts = append(p.Facts, briefFact{Label: fact.Label, Value: fact.Value})
			}
			brief.Parts = append(brief.Parts, p)
		}
		input, err := renderRunBrief(brief)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]string, 0, len(skills))
		for _, skill := range skills {
			out = append(out, map[string]string{"name": skill.Name, "body": skill.Body})
		}
		return map[string]any{"input": input, "skills": out, "pointers": pointer, "brief_schema": spawnBriefSchema,
			"omitted_parts": omitted, "omitted_facts": omittedFacts}, nil
	case "thesis":
		err := requireThesisProse(request.Thesis)
		return evalValidation(err), nil
	case "artifact":
		id := &RunIdentity{RunID: request.RunID, Tier: "pipeline", Role: "packager", Mounts: RunMounts{AllowWrite: request.Planes}}
		err := requirePackagedRender(id, 1, request.Path)
		return evalValidation(err), nil
	case "repository":
		git := repository.Git{Root: request.Repository}
		head, err := git.Commit(context.Background(), "HEAD")
		if err == nil {
			err = git.Clean(context.Background())
		}
		if err == nil && head != request.StartCommit {
			err = fmt.Errorf("the Packager must preserve its assigned submitted version")
		}
		return evalValidation(err), nil
	default:
		return nil, fmt.Errorf("the evaluation contract operation is unsupported")
	}
}

// storedScribeFact reports whether a stored case brief holds this Git fact
// while the Packager brief of the product holds no such fact.
func storedScribeFact(label string) bool {
	switch label {
	case "Verified source commit", "Scribe input commit", "Scribe result commit", "Scribe state", "Wiki source baseline":
		return true
	}
	return false
}

func evalValidation(err error) map[string]any {
	if err != nil {
		return map[string]any{"accepted": false, "reason": err.Error()}
	}
	return map[string]any{"accepted": true}
}
