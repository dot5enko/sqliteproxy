package management

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/dot5enko/cloudfunctions/packages/sqlite/storage"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// Handler serves the HTTP management API.
type Handler struct {
	store *storage.Store
}

// NewHandler creates a management API handler.
func NewHandler(store *storage.Store) *Handler {
	return &Handler{store: store}
}

// ServeHTTP routes management API requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}

	switch {
	case path == "/v1/databases" && r.Method == http.MethodPost:
		h.createDatabase(w, r)
	case path == "/v1/databases" && r.Method == http.MethodGet:
		h.listDatabases(w, r)
	case path == "/v1/databases" && r.Method != http.MethodGet && r.Method != http.MethodPost:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	case strings.HasPrefix(path, "/v1/databases/"):
		name := strings.TrimPrefix(path, "/v1/databases/")
		if name == "" || strings.Contains(name, "/") {
			writeError(w, http.StatusNotFound, "database_not_found", "database not found")
			return
		}
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		h.getDatabase(w, r, name)
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

type createRequest struct {
	Label string `json:"label"`
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	db, err := h.store.Create(req.Label)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidLabel) {
			writeError(w, http.StatusBadRequest, "invalid_label", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create database")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/v1/databases/"+db.Name)
	writeJSON(w, http.StatusCreated, db.Details())
}

func (h *Handler) listDatabases(w http.ResponseWriter, r *http.Request) {
	list, err := h.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list databases")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"databases": list})
}

func (h *Handler) getDatabase(w http.ResponseWriter, r *http.Request, name string) {
	db, err := h.store.Get(name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "database_not_found", "database not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get database")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, db.Details())
}

func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()

	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	data, err := io.ReadAll(body)
	if err != nil {
		return errors.New("request body too large or unreadable")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("invalid JSON body")
	}
	if dec.More() {
		return errors.New("invalid JSON body: trailing data")
	}
	return nil
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var payload apiError
	payload.Error.Code = code
	payload.Error.Message = message
	writeJSON(w, status, payload)
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(payload)
}
