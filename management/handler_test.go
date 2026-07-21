package management_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dot5enko/cloudfunctions/packages/sqlite/management"
	"github.com/dot5enko/cloudfunctions/packages/sqlite/storage"
)

func TestManagementAPICreateListInspect(t *testing.T) {
	store, err := storage.Open(storage.StoreConfig{
		Root:        t.TempDir(),
		BusyTimeout: time.Second,
		MaxConns:    2,
		WALMode:     true,
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	h := management.NewHandler(store)

	req := httptest.NewRequest(http.MethodPost, "/v1/databases", bytes.NewBufferString(`{"label":"orders"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected no-store cache control")
	}

	var created storage.Details
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Name == "" || created.Username == "" || created.Password == "" {
		t.Fatalf("missing credentials: %+v", created)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/databases", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), created.Password) {
		t.Fatalf("list leaked password: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/databases/"+created.Name, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inspect status=%d body=%s", rec.Code, rec.Body.String())
	}
	var details storage.Details
	if err := json.Unmarshal(rec.Body.Bytes(), &details); err != nil {
		t.Fatalf("decode inspect: %v", err)
	}
	if details.Password != created.Password {
		t.Fatalf("inspect should return password")
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/databases/missing", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/databases", bytes.NewBufferString(`{"unknown":true}`))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown fields, got %d", rec.Code)
	}
}
