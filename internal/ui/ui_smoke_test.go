package ui

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestTemplatesRender(t *testing.T) {
	u, err := New()
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	base := map[string]any{
		"CSS":     "/assets/css/output.css",
		"JS":      "/assets/js/output.js",
		"Favicon": template.URL("data:,"),
		"Version": "vX.X.X",
	}
	pages := map[string]map[string]any{
		"login.html": {
			"Title":         "Login",
			"Error":         "invalid password",
			"NoCredentials": true,
			"AppName":       "sprout",
		},
		"settings.html": {
			"Title":     "Settings",
			"LogLevel":  "info",
			"UIBind":    ":8484",
			"ProxyBind": "",
		},
	}
	for name, extra := range pages {
		data := map[string]any{}
		for k, v := range base {
			data[k] = v
		}
		for k, v := range extra {
			data[k] = v
		}
		var buf bytes.Buffer
		if err := u.Execute(&buf, name, data); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		out := buf.String()
		if !strings.Contains(out, "<!DOCTYPE html>") || !strings.Contains(out, "</html>") {
			t.Fatalf("render %s: missing page shell", name)
		}
	}
}
