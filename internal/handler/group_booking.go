package handler

import "net/http"

// GetDynamicGroupSlots handles GET /v1/public/group/{handles}/slots.
func (h *Handler) GetDynamicGroupSlots(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, http.StatusGone, "create a new single-use dynamic link from the scheduling app")
}

// CreateDynamicGroupBooking handles POST /v1/public/group/{handles}/bookings.
func (h *Handler) CreateDynamicGroupBooking(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, http.StatusGone, "create a new single-use dynamic link from the scheduling app")
}

// DynamicGroupBookPage handles GET /u/{handle} when handle contains '+'.
func (h *Handler) DynamicGroupBookPage(w http.ResponseWriter, r *http.Request) {
	h.writeError(w, http.StatusGone, "create a new single-use dynamic link from the scheduling app")
}
