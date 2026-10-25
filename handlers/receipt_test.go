package handlers

import (
	"encoding/json"
	"errors"
	"testing"

	"kitchen_manager/internal/testutil"
)

func TestParseReceiptResponseToolUse(t *testing.T) {
	body := `{"stop_reason":"tool_use","content":[{"type":"text","text":"ok"},{"type":"tool_use","name":"record_receipt_items","input":{"items":[
		{"name":" milk ","plu":"1","quantity":1,"unit":"piece","unit_cost_cents":300,"quantity_per_scan":5},
		{"name":"beef","plu":"","quantity":2.5,"unit":"lb","unit_cost_cents":499}]}}]}`
	items, err := parseReceiptResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "milk" || items[0].QuantityPerScan != 0 || items[1].Quantity != 2.5 || items[1].UnitCostCents != 499 {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestParseReceiptResponseTruncated(t *testing.T) {
	for _, body := range []string{
		`{"stop_reason":"max_tokens","content":[{"type":"tool_use","name":"record_receipt_items","input":{"items":[{"name":"a"}]}}]}`,
		`{"stop_reason":"max_tokens","content":[]}`,
		`{"stop_reason":"max_tokens","content":[{"type":"text","text":"hi"}]}`,
	} {
		if _, err := parseReceiptResponse([]byte(body)); !errors.Is(err, errReceiptTruncated) {
			t.Errorf("want truncated error for %s, got %v", body, err)
		}
	}
}

func TestParseReceiptResponseNoToolUse(t *testing.T) {
	for _, body := range []string{
		`{"stop_reason":"end_turn","content":[{"type":"text","text":"[]"}]}`,
		`{"stop_reason":"refusal","content":[]}`,
	} {
		_, err := parseReceiptResponse([]byte(body))
		if err == nil || errors.Is(err, errReceiptTruncated) {
			t.Errorf("want non-truncation error for %s, got %v", body, err)
		}
	}
}

func TestParseReceiptResponseEmptyItems(t *testing.T) {
	for _, in := range []string{`{"items":[]}`, `{}`} {
		body := `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"record_receipt_items","input":` + in + `}]}`
		items, err := parseReceiptResponse([]byte(body))
		if err != nil || items == nil || len(items) != 0 {
			t.Errorf("%s: items=%v err=%v", in, items, err)
		}
	}
}

func TestParseReceiptResponseMalformed(t *testing.T) {
	if _, err := parseReceiptResponse([]byte(`not json`)); err == nil {
		t.Error("want error for bad envelope")
	}
	body := `{"stop_reason":"tool_use","content":[{"type":"tool_use","name":"record_receipt_items","input":{"items":"x"}}]}`
	if _, err := parseReceiptResponse([]byte(body)); err == nil {
		t.Error("want error for wrong items type")
	}
}

func TestBuildReceiptRequest(t *testing.T) {
	b, err := json.Marshal(buildReceiptRequest("m1", "image/jpeg", "QUJD"))
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Model      string `json:"model"`
		MaxTokens  int    `json:"max_tokens"`
		ToolChoice struct {
			Type, Name string
		} `json:"tool_choice"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Messages []struct {
			Content []struct {
				Source struct {
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Model != "m1" || r.MaxTokens != receiptMaxTokens || r.ToolChoice.Type != "tool" || r.ToolChoice.Name != receiptToolName ||
		len(r.Tools) != 1 || r.Tools[0].Name != receiptToolName ||
		r.Messages[0].Content[0].Source.MediaType != "image/jpeg" || r.Messages[0].Content[0].Source.Data != "QUJD" {
		t.Fatalf("unexpected request: %s", b)
	}
}

func TestReceiptModelEnv(t *testing.T) {
	for _, v := range []string{"", "   "} {
		t.Setenv("ANTHROPIC_MODEL", v)
		if got := receiptModel(); got != defaultAnthropicModel {
			t.Errorf("%q: got %q", v, got)
		}
	}
	t.Setenv("ANTHROPIC_MODEL", "claude-x")
	if got := receiptModel(); got != "claude-x" {
		t.Errorf("got %q", got)
	}
}

func TestReceiptPLULookupViaAlias(t *testing.T) {
	db := testutil.NewDB(t)
	if _, err := db.Exec(`INSERT INTO inventory (name, barcode, quantity_per_scan) VALUES ('milk','111',2)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO inventory (name, barcode, quantity_per_scan) VALUES ('flour','',1)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO inventory_skus (inventory_id, barcode, quantity_per_scan) VALUES (?,?,3)`, id, "222"); err != nil {
		t.Fatal(err)
	}
	name, qps, ok, err := lookupReceiptPLU(db, "222")
	if err != nil || !ok || name != "flour" || qps != 3 {
		t.Fatalf("alias lookup: %q %v %v %v", name, qps, ok, err)
	}
	items := []ReceiptItem{
		{Name: "a", PLU: "111"}, {Name: "b", PLU: "222"}, {Name: "c", PLU: "999"}, {Name: "d"},
	}
	enrichReceiptItems(db, items)
	if items[0].Name != "milk" || items[0].QuantityPerScan != 2 ||
		items[1].Name != "flour" || items[1].QuantityPerScan != 3 ||
		items[2].Name != "c" || items[2].QuantityPerScan != 0 ||
		items[3].Name != "d" {
		t.Fatalf("unexpected: %+v", items)
	}
}
