// Package contract confere o OpenAPI e o esquema de eventos (G5) contra o que o serviço registra:
// uma rota que existe só num dos lados, ou um $ref quebrado, falha o CI.
package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/httpapi"
	"github.com/MarceloVilela/microservices-go/ms-api/internal/adapters/sse"
)

var specs = filepath.Join("..", "..", "..", "specs")

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(specs, "openapi-api.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("openapi-api.yaml não é YAML válido: %v", err)
	}
	return doc
}

func TestOpenAPIRotasIguaisAsDoServico(t *testing.T) {
	doc := loadOpenAPI(t)
	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	var fromSpec []string
	for path, ops := range doc["paths"].(map[string]any) {
		for method, op := range ops.(map[string]any) {
			if _, ok := op.(map[string]any)["responses"]; !ok {
				t.Errorf("%s %s sem responses", method, path)
			}
			fromSpec = append(fromSpec, strings.ToUpper(method)+" "+path)
		}
	}
	fromCode := append(slices.Clone(httpapi.Routes), sse.Route)
	sort.Strings(fromSpec)
	sort.Strings(fromCode)
	if !slices.Equal(fromSpec, fromCode) {
		t.Errorf("rotas divergem\n  openapi: %v\n  serviço: %v", fromSpec, fromCode)
	}
}

func TestOpenAPIRefsResolvem(t *testing.T) {
	doc := loadOpenAPI(t)
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				cur := any(doc)
				for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
					m, isMap := cur.(map[string]any)
					if !isMap || m[part] == nil {
						t.Errorf("$ref quebrado: %s", ref)
						return
					}
					cur = m[part]
				}
			}
			for _, c := range x {
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(doc)
}

func TestEsquemaDeEventosCobreOsTiposEmitidos(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(specs, "events.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Type struct {
				Enum []string `json:"enum"`
			} `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("events.schema.json inválido: %v", err)
	}
	// tipos gravados em `events` pelo ingest (batch.created), worker (photo.*, batch.completed) e ms-api (photo.retried)
	emitted := []string{"batch.created", "photo.done", "photo.failed", "photo.retried", "batch.completed"}
	got := slices.Clone(schema.Properties.Type.Enum)
	sort.Strings(got)
	sort.Strings(emitted)
	if !slices.Equal(got, emitted) {
		t.Errorf("tipos do esquema %v != emitidos %v", got, emitted)
	}
}
