package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// PromptSet holds compiled templates for the service.
type PromptSet struct {
	Plan           *template.Template
	ExecuteStep    *template.Template
	ProposeCommand *template.Template
	Unblock        *template.Template
	Verify         *template.Template
}

// LoadPrompts loads templates from the prompts directory.
func LoadPrompts(dir string) (*PromptSet, error) {
	load := func(name string) (*template.Template, error) {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		tmpl, err := template.New(name).Parse(string(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		return tmpl, nil
	}
	plan, err := load("plan.tmpl")
	if err != nil {
		return nil, err
	}
	exec, err := load("execute_step.tmpl")
	if err != nil {
		return nil, err
	}
	cmd, err := load("propose_command.tmpl")
	if err != nil {
		return nil, err
	}
	unblock, err := load("unblock.tmpl")
	if err != nil {
		return nil, err
	}
	verify, err := load("verify.tmpl")
	if err != nil {
		return nil, err
	}
	return &PromptSet{
		Plan:           plan,
		ExecuteStep:    exec,
		ProposeCommand: cmd,
		Unblock:        unblock,
		Verify:         verify,
	}, nil
}

func renderPrompt(t *template.Template, data any) (string, error) {
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		return "", err
	}
	return sb.String(), nil
}
