package output

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vladcuciureanu/betty/internal/domain"
)

func TestJSONEnvelope(t *testing.T) {
	var out bytes.Buffer
	r := Renderer{Out: &out, Err: &out, Mode: JSON}
	if err := r.Data(map[string]string{"value": "ok"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["schema_version"] != float64(1) {
		t.Fatalf("schema_version = %v", got["schema_version"])
	}
}
func TestJSONErrorEnvelope(t *testing.T) {
	var out bytes.Buffer
	r := Renderer{Out: &out, Err: &out, Mode: JSON}
	r.Failure(domain.NewError(domain.CodeNotFound, "gone"))
	var got struct {
		Error domain.Error `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != domain.CodeNotFound {
		t.Fatalf("code = %q", got.Error.Code)
	}
}
