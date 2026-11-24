package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
