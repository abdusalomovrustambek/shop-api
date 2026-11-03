package catalog

import (
	"encoding/base64"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/rustambekabdusalomov/shop-api/internal/httpx"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

// Page is one page of products. NextCursor is nil on the last page.
type Page struct {
	Items      []Product `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

// Handler serves the catalog routes.
type Handler struct {
	repo *Repo
	log  *slog.Logger
}

// NewHandler returns a Handler reading from repo.
func NewHandler(repo *Repo, log *slog.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

// Register adds the catalog routes to rt.
func (h *Handler) Register(rt *httpx.Router) {
	rt.Handle(http.MethodGet, "/api/products", h.List, httpx.AuthNone)
	rt.Handle(http.MethodGet, "/api/products/{id}", h.Get, httpx.AuthNone)
}

// List answers GET /api/products?search=&limit=&cursor=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultLimit
	if s := query.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "limit must be an integer between 1 and %d", maxLimit))
			return
		}
		limit = n
	}
	after, err := decodeCursor(query.Get("cursor"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	// One extra row tells whether there is a next page.
	var products []Product
	search := query.Get("search")
	if search != "" {
		products, err = h.repo.Search(r.Context(), search, after, limit+1)
		if err != nil {
			httpx.Fail(w, h.log, "search failed", err, "search", search)
			return
		}
	} else {
		products, err = h.repo.List(r.Context(), after, limit+1)
		if err != nil {
			httpx.Fail(w, h.log, "list products failed", err)
			return
		}
	}

	page := Page{Items: products}
	if len(products) > limit {
		page.Items = products[:limit]
		cursor := encodeCursor(page.Items[limit-1].ID)
		page.NextCursor = &cursor
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

// Get answers GET /api/products/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpx.WriteError(w, httpx.Errorf(httpx.ErrInvalid, "product id must be an integer"))
		return
	}
	product, err := h.repo.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, h.log, "get product failed", err, "product_id", id)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, product)
}

func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

// decodeCursor returns the id after which the next page starts; an empty cursor is the start.
func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, httpx.Errorf(httpx.ErrInvalid, "invalid cursor")
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id < 0 {
		return 0, httpx.Errorf(httpx.ErrInvalid, "invalid cursor")
	}
	return id, nil
}
