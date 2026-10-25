package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"kitchen_manager/internal/testutil"
)

func TestBoughtBroadcasts(t *testing.T) {
	db := testutil.NewDB(t)
	hub := NewHub()
	mux := http.NewServeMux()
	RegisterShopping(mux, db, hub)
	var sid int64
	if err := db.QueryRow(`INSERT INTO shopping_list (name,quantity_needed,unit,checked,source) VALUES ('rice',1,'g',0,'manual') RETURNING id`).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/shopping/"+strconv.FormatInt(sid, 10)+"/bought", bytes.NewBufferString(""))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var got []string
	for {
		select {
		case m := <-hub.broadcast:
			got = append(got, string(m))
			continue
		default:
		}
		break
	}
	all := strings.Join(got, "|")
	if !strings.Contains(all, "inventory_updated") || !strings.Contains(all, "shopping_updated") {
		t.Fatal(all)
	}
}
