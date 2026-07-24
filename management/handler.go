package management

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	storage "github.com/dot5enko/sqliteproxy/storage"
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

	case path == "/v1/databases/import" && r.Method == http.MethodPost:
		h.importDatabase(w, r)

	case strings.HasPrefix(path, "/v1/databases/"):
		parts := strings.TrimPrefix(path, "/v1/databases/")
		if parts == "" {
			writeError(w, http.StatusNotFound, "database_not_found", "database not found")
			return
		}

		slashIdx := strings.Index(parts, "/")
		if slashIdx < 0 {
			// /v1/databases/{name} — GET only
			if r.Method != http.MethodGet {
				writeMethodNotAllowed(w, http.MethodGet)
				return
			}
			h.getDatabase(w, r, parts)
			return
		}

		name := parts[:slashIdx]
		action := parts[slashIdx+1:]
		if name == "" || action == "" {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}

		switch {
		case action == "export" && r.Method == http.MethodGet:
			h.exportDatabase(w, r, name)
		case action == "query" && r.Method == http.MethodPost:
			h.queryDatabase(w, r, name)
		default:
			writeError(w, http.StatusNotFound, "not_found", "not found")
		}

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

func (h *Handler) exportDatabase(w http.ResponseWriter, r *http.Request, name string) {
	path, err := h.store.Export(name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "database_not_found", "database not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to export database")
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+".sqlite\"")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

func (h *Handler) importDatabase(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "multipart form required")
		return
	}

	label := r.FormValue("label")

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "missing file field")
		return
	}
	defer file.Close()

	fileData, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "failed to read uploaded file")
		return
	}

	db, err := h.store.Import(label, fileData)
	if err != nil {
		writeError(w, http.StatusBadRequest, "import_failed", err.Error())
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/v1/databases/"+db.Name)
	writeJSON(w, http.StatusCreated, db.Details())
}

func (h *Handler) queryDatabase(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Query string `json:"query"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "query is required")
		return
	}

	columns, rows, err := h.store.Query(name, req.Query)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "database_not_found", "database not found")
			return
		}
		writeError(w, http.StatusBadRequest, "query_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"columns": columns,
		"rows":    rows,
	})
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
