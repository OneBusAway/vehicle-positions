package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVehicleText_HeldToItsLengthEverywhere: the vehicle form, the API and the
// CSV import take a label and an agency tag of the same length, counted in
// characters, so what one refuses another does not save.
func TestVehicleText_HeldToItsLengthEverywhere(t *testing.T) {
	texts := []struct {
		name             string
		label, agencyTag string
		problem          string
	}{
		{name: "as long as they may be", label: strings.Repeat("l", maxFieldLength), agencyTag: strings.Repeat("t", maxFieldLength)},
		{name: "a label as long as it may be in Devanagari", label: strings.Repeat("ब", maxFieldLength)},
		{name: "an agency tag as long as it may be in Devanagari", agencyTag: strings.Repeat("ब", maxFieldLength)},
		{name: "a label too long", label: strings.Repeat("l", maxFieldLength+1), problem: "label must be at most 255 characters"},
		{name: "an agency tag too long", agencyTag: strings.Repeat("t", maxFieldLength+1), problem: "agency_tag must be at most 255 characters"},
		{name: "a label too long in Devanagari", label: strings.Repeat("ब", maxFieldLength+1), problem: "label must be at most 255 characters"},
	}
	for _, tt := range texts {
		form := url.Values{"id": {"bus-1"}, "label": {tt.label}, "agency_tag": {tt.agencyTag}}
		post := func(t *testing.T, path string, store *fakeVehicleStore) *httptest.ResponseRecorder {
			ui := newTestAdminUI(t)
			wireFakeVehicleStore(ui, store)
			mux := http.NewServeMux()
			registerAdminUI(mux, ui)
			return postAdminForm(t, mux, path, form.Encode())
		}
		// refused is the form shown again with what was typed and why.
		refused := func(t *testing.T, w *httptest.ResponseRecorder) {
			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			assert.Contains(t, w.Body.String(), "The vehicle was not saved.</b> "+tt.problem)
			if tt.label != "" {
				assert.Contains(t, w.Body.String(), `value="`+tt.label+`"`)
			}
			if tt.agencyTag != "" {
				assert.Contains(t, w.Body.String(), `value="`+tt.agencyTag+`"`)
			}
		}

		t.Run("new vehicle form, "+tt.name, func(t *testing.T) {
			store := newFakeVehicleStore()
			w := post(t, "/admin/vehicles", store)
			if tt.problem != "" {
				refused(t, w)
				assert.Contains(t, w.Body.String(), `value="bus-1"`)
				assert.Empty(t, store.vehicles)
				return
			}
			require.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, tt.label, store.vehicles["bus-1"].Label)
			assert.Equal(t, tt.agencyTag, store.vehicles["bus-1"].AgencyTag)
		})
		t.Run("edit form, "+tt.name, func(t *testing.T) {
			store := newFakeVehicleStore(VehicleResponse{ID: "bus-1", Label: "Old label", AgencyTag: "old-tag", Active: true})
			w := post(t, "/admin/vehicles/bus-1", store)
			if tt.problem != "" {
				refused(t, w)
				assert.Contains(t, w.Body.String(), `action="/admin/vehicles/bus-1"`, "still the edit form")
				assert.Equal(t, "Old label", store.vehicles["bus-1"].Label)
				assert.Equal(t, "old-tag", store.vehicles["bus-1"].AgencyTag)
				return
			}
			require.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, tt.label, store.vehicles["bus-1"].Label)
			assert.Equal(t, tt.agencyTag, store.vehicles["bus-1"].AgencyTag)
		})
		t.Run("API and import, "+tt.name, func(t *testing.T) {
			req := upsertVehicleRequest{ID: "bus-1", Label: tt.label, AgencyTag: tt.agencyTag}
			_, views := vehicleRows([]importRecord{{Line: 2, Cells: map[string]string{"id": "bus-1", "label": tt.label, "agency_tag": tt.agencyTag}}})
			require.Len(t, views, 1)
			if tt.problem != "" {
				assert.EqualError(t, req.validate(), tt.problem)
				assert.Equal(t, tt.problem, views[0].Reason)
				return
			}
			assert.NoError(t, req.validate())
			assert.Empty(t, views[0].Reason)
		})
	}
}

// TestVehicleID_OnlyDotsIsRefusedEverywhere: the API, the new vehicle form and
// the CSV import refuse an id a path can't hold, and take the ids nearest it.
func TestVehicleID_OnlyDotsIsRefusedEverywhere(t *testing.T) {
	const problem = "vehicle id must not be only dots"
	tests := []struct {
		id      string
		refused bool
	}{
		{".", true},
		{"..", true},
		{"...", false},
		{".bus", false},
		{"bus.", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			ui := newTestAdminUI(t)
			store := newFakeVehicleStore()
			wireFakeVehicleStore(ui, store)
			mux := http.NewServeMux()
			registerAdminUI(mux, ui)
			form := postAdminForm(t, mux, "/admin/vehicles", url.Values{"id": {tt.id}}.Encode())
			_, views := vehicleRows([]importRecord{{Line: 2, Cells: map[string]string{"id": tt.id}}})
			require.Len(t, views, 1)
			err := (&upsertVehicleRequest{ID: tt.id}).validate()

			if tt.refused {
				assert.EqualError(t, err, problem)
				assert.Equal(t, http.StatusUnprocessableEntity, form.Code)
				assert.Contains(t, form.Body.String(), problem)
				assert.Empty(t, store.vehicles)
				assert.Equal(t, problem, views[0].Reason)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, http.StatusSeeOther, form.Code)
			assert.Contains(t, store.vehicles, tt.id)
			assert.Empty(t, views[0].Reason)
		})
	}
}

// TestVehicleID_OnlyDotsCanStillBeDeactivated: a vehicle saved with such an id
// before can still be deactivated, through the API by a client that sends the
// dots escaped.
func TestVehicleID_OnlyDotsCanStillBeDeactivated(t *testing.T) {
	store := newMockVehicleStore()
	tracker := NewTracker(5 * time.Minute)
	t.Cleanup(tracker.Stop)
	mux := http.NewServeMux()
	mux.Handle("DELETE /api/v1/admin/vehicles/{id}", handleDeactivateVehicle(store, tracker))
	for id, path := range map[string]string{".": "%2e", "..": "%2e%2e"} {
		store.vehicles[id] = &VehicleResponse{ID: id, Active: true}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/vehicles/"+path, nil))
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		assert.False(t, store.vehicles[id].Active)
	}
}
