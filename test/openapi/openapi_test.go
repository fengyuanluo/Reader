package openapi_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIDocumentIsValid(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate openapi document: %v", err)
	}
}

func TestBookSourceImportExamplesValidateAgainstSchema(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate openapi document: %v", err)
	}
	schema := doc.Components.Schemas["ImportBookSourcesRequest"]
	if schema == nil {
		t.Fatal("ImportBookSourcesRequest schema is missing")
	}

	for _, name := range []string{
		"book-source-import-single.json",
		"book-source-import-array.json",
		"book-source-import-remote.json",
		"book-source-import-sourceurls.json",
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "api", "examples", name))
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatalf("decode example: %v", err)
			}
			if err := schema.Value.VisitJSON(value); err != nil {
				t.Fatalf("example does not match schema: %v", err)
			}
		})
	}
}

func TestOpenAPIDoesNotExposeLegacyLegadoRoutes(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	legacyRoutes := []string{
		"/saveBookSource",
		"/saveBookSources",
		"/getBookSource",
		"/getBookSources",
		"/searchBook",
		"/getBookContent",
	}
	for _, route := range legacyRoutes {
		if doc.Paths.Find(route) != nil {
			t.Fatalf("legacy route %s must not be exposed in Reader OpenAPI", route)
		}
	}
}

func TestOpenAPIDoesNotUseLegacyReturnDataEnvelope(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	for name, schemaRef := range doc.Components.Schemas {
		if schemaRef == nil || schemaRef.Value == nil {
			continue
		}
		_, hasIsSuccess := schemaRef.Value.Properties["isSuccess"]
		_, hasErrorMsg := schemaRef.Value.Properties["errorMsg"]
		_, hasData := schemaRef.Value.Properties["data"]
		if hasIsSuccess || hasErrorMsg || (hasData && hasIsSuccess && hasErrorMsg) {
			t.Fatalf("schema %s resembles legacy ReturnData envelope", name)
		}
	}
}

func TestOpenAPICommonErrorResponsesHaveExamples(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	for _, name := range []string{"BadRequest", "Unauthorized", "NotFound", "InternalError"} {
		response := doc.Components.Responses[name]
		if response == nil || response.Value == nil {
			t.Fatalf("response %s is missing", name)
		}
		media := response.Value.Content.Get("application/json")
		if media == nil {
			t.Fatalf("response %s missing application/json media", name)
		}
		if media.Example == nil {
			t.Fatalf("response %s missing example", name)
		}
	}
}

func TestBookSourceExamplesCoverMinimalAndCompleteRules(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	minimal := doc.Components.Examples["MinimalBookSource"]
	if minimal == nil || minimal.Value == nil || minimal.Value.Value == nil {
		t.Fatal("MinimalBookSource example is missing")
	}
	complete := doc.Components.Examples["CompleteBookSource"]
	if complete == nil || complete.Value == nil || complete.Value.Value == nil {
		t.Fatal("CompleteBookSource example is missing")
	}
	value, ok := complete.Value.Value.(map[string]any)
	if !ok {
		t.Fatalf("CompleteBookSource value type = %T, want map[string]any", complete.Value.Value)
	}
	for _, field := range []string{"ruleSearch", "ruleBookInfo", "ruleToc", "ruleContent"} {
		if value[field] == nil {
			t.Fatalf("CompleteBookSource missing %s", field)
		}
	}
}

func TestSearchExamplesAndWarningSchemaExist(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	for _, name := range []string{"SearchSingleSource", "SearchMultiSource"} {
		example := doc.Components.Examples[name]
		if example == nil || example.Value == nil || example.Value.Value == nil {
			t.Fatalf("%s example is missing", name)
		}
	}
	warning := doc.Components.Schemas["SourceWarning"]
	if warning == nil || warning.Value == nil {
		t.Fatal("SourceWarning schema is missing")
	}
	for _, field := range []string{"sourceId", "code", "message"} {
		if _, ok := warning.Value.Properties[field]; !ok {
			t.Fatalf("SourceWarning missing %s", field)
		}
	}
}

func TestBookAndChapterSchemasExist(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	for _, name := range []string{"Book", "Chapter", "ChapterContentResponse", "BookProgress", "BookFromSearch"} {
		switch name {
		case "BookFromSearch":
			example := doc.Components.Examples[name]
			if example == nil || example.Value == nil || example.Value.Value == nil {
				t.Fatalf("%s example is missing", name)
			}
		default:
			schema := doc.Components.Schemas[name]
			if schema == nil || schema.Value == nil {
				t.Fatalf("%s schema is missing", name)
			}
		}
	}
	bookSchema := doc.Components.Schemas["Book"]
	if bookSchema == nil || bookSchema.Value == nil {
		t.Fatal("Book schema is missing")
	}
	if _, ok := bookSchema.Value.Properties["downloadUrls"]; !ok {
		t.Fatal("Book schema missing downloadUrls")
	}
}

func TestReplaceRuleSchemasAndExamplesExist(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	for _, name := range []string{"ReplaceRule", "ReplaceRuleTestRequest", "ReplaceRuleTestResponse", "ReplaceRuleListResponse"} {
		schema := doc.Components.Schemas[name]
		if schema == nil || schema.Value == nil {
			t.Fatalf("%s schema is missing", name)
		}
	}
	for _, name := range []string{"PlainReplaceRule", "RegexReplaceRule"} {
		example := doc.Components.Examples[name]
		if example == nil || example.Value == nil || example.Value.Value == nil {
			t.Fatalf("%s example is missing", name)
		}
	}
}

func TestDebugEventSchemaAndKeywordExampleExist(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	event := doc.Components.Schemas["DebugEvent"]
	if event == nil || event.Value == nil {
		t.Fatal("DebugEvent schema is missing")
	}
	for _, field := range []string{"stage", "message", "elapsedMs", "traceId"} {
		if _, ok := event.Value.Properties[field]; !ok {
			t.Fatalf("DebugEvent missing %s", field)
		}
	}
	example := doc.Components.Examples["KeywordDebugSession"]
	if example == nil || example.Value == nil || example.Value.Value == nil {
		t.Fatal("KeywordDebugSession example is missing")
	}
}

func TestCheckFailureCodesCoverRequiredCategories(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	schema := doc.Components.Schemas["CheckFailureCode"]
	if schema == nil || schema.Value == nil {
		t.Fatal("CheckFailureCode schema is missing")
	}
	values := map[string]bool{}
	for _, value := range schema.Value.Enum {
		if str, ok := value.(string); ok {
			values[str] = true
		}
	}
	for _, required := range []string{"timeout", "js_failed", "toc_failed", "content_failed"} {
		if !values[required] {
			t.Fatalf("CheckFailureCode missing %s", required)
		}
	}
}

func TestCheckTaskItemSchemaIncludesFailureClass(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	schema := doc.Components.Schemas["CheckTaskItem"]
	if schema == nil || schema.Value == nil {
		t.Fatal("CheckTaskItem schema is missing")
	}
	if _, ok := schema.Value.Properties["failureClass"]; !ok {
		t.Fatal("CheckTaskItem missing failureClass")
	}
}

func TestOpenAPICoversAllCorePhaseEndpoints(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	required := map[string][]string{
		"/book-sources/import":                     {"POST"},
		"/book-sources":                            {"GET", "POST"},
		"/book-sources/{sourceId}":                 {"GET", "PUT", "DELETE"},
		"/book-sources/{sourceId}/enabled":         {"PATCH"},
		"/book-sources/{sourceId}/explore-enabled": {"PATCH"},
		"/book-sources/reorder":                    {"PATCH"},
		"/search":                                  {"POST"},
		"/discovery":                               {"POST"},
		"/books":                                   {"GET", "POST"},
		"/books/{bookId}":                          {"GET", "DELETE"},
		"/books/{bookId}/refresh-info":             {"POST"},
		"/books/{bookId}/refresh-toc":              {"POST"},
		"/books/{bookId}/chapters":                 {"GET"},
		"/books/{bookId}/chapters/{index}/content": {"GET"},
		"/books/{bookId}/progress":                 {"PUT"},
		"/images/proxy":                            {"GET"},
		"/replace-rules":                           {"GET", "POST"},
		"/replace-rules/{ruleId}":                  {"PUT", "DELETE"},
		"/replace-rules/test":                      {"POST"},
		"/debug/book-source":                       {"POST"},
		"/debug/sessions/{sessionId}":              {"GET", "DELETE"},
		"/debug/sessions/{sessionId}/events":       {"GET"},
		"/check/book-sources":                      {"POST"},
		"/check/tasks/{taskId}":                    {"GET", "DELETE"},
		"/check/tasks/{taskId}/events":             {"GET"},
	}

	for path, methods := range required {
		item := doc.Paths.Find(path)
		if item == nil {
			t.Fatalf("required path %s is missing", path)
		}
		for _, method := range methods {
			if item.GetOperation(method) == nil {
				t.Fatalf("required operation %s %s is missing", method, path)
			}
		}
	}
}

func TestImageProxyContractUsesBinaryImageResponsesAndBearerAuth(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("load openapi document: %v", err)
	}

	op := doc.Paths.Find("/images/proxy").Get
	if op == nil {
		t.Fatal("GET /images/proxy is missing")
	}
	if op.Security == nil || len(*op.Security) == 0 {
		t.Fatal("GET /images/proxy must require bearer auth")
	}
	response := op.Responses.Value("200")
	if response == nil || response.Value == nil {
		t.Fatal("GET /images/proxy missing 200 response")
	}
	for _, mediaType := range []string{"image/png", "image/jpeg", "image/webp", "image/gif", "image/svg+xml", "application/octet-stream"} {
		media := response.Value.Content.Get(mediaType)
		if media == nil || media.Schema == nil || media.Schema.Value == nil {
			t.Fatalf("GET /images/proxy missing %s response schema", mediaType)
		}
		if media.Schema.Value.Format != "binary" {
			t.Fatalf("GET /images/proxy %s format = %q, want binary", mediaType, media.Schema.Value.Format)
		}
	}
}
