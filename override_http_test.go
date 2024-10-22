package block

import (
	"bytes"
	"encoding/json"
	"github.com/gorilla/mux"
	"net/http"
	"net/http/httptest"
	"testing"
  "fmt"
)

func TestModifyOverrideList(t *testing.T) {
  CONFIG_PATH = "./test_data/block_rules.json"

	// Create a new test block
	b := New()
	b.superapi_enabled = true
	b.loadSPRConfig()

	// Create a new router and register the route
	r := mux.NewRouter()
	r.HandleFunc("/config", b.showConfig).Methods("GET")
	r.HandleFunc("/override/{list}", b.modifyOverrideDomains).Methods("PUT", "DELETE")
	r.HandleFunc("/overrideList/{list}", b.modifyOverrideList).Methods("PUT", "DELETE")

	// Test cases
	testCases := []struct {
		name           string
		method         string
		listName       string
		payload        OverrideList
		expectedStatus int
		// Add more fields as needed for your specific test cases
	}{
		{
			name:     "Create a list",
			method:   "PUT",
			listName: "testlist",
			payload: OverrideList{
				Enabled: true,
				Name:    "testlist",
				Tags:    []string{"tag1", "tag2"},
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:     "Delete testlist",
			method:   "DELETE",
			listName: "testlist",
			payload: OverrideList{
				Enabled: true,
				Name:    "testlist",
				Tags:    []string{"tag1", "tag2"},
			},
			expectedStatus: http.StatusOK,
		},
    {
			name:     "Second Delete Fails",
			method:   "DELETE",
			listName: "testlist",
			payload: OverrideList{
				Enabled: true,
				Name:    "testlist",
				Tags:    []string{"tag1", "tag2"},
			},
			expectedStatus: 404,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new HTTP request
			payload, _ := json.Marshal(tc.payload)
			req, err := http.NewRequest(tc.method, "/overrideList/"+tc.listName, bytes.NewBuffer(payload))
			if err != nil {
				t.Fatal(err)
			}

			// Create a ResponseRecorder (implements http.ResponseWriter) to record the response
			rr := httptest.NewRecorder()

			// Call the handler directly
			r.ServeHTTP(rr, req)

			// Check the status code
			if status := rr.Code; status != tc.expectedStatus {
				t.Errorf("testing %v: handler returned wrong status code: got %v want %v: body %v",
					tc.name, status, tc.expectedStatus, rr.Body)
			}
			// Add more assertions based on your specific requirements
			// For example, checking response body, headers, etc.
		})
	}

  fmt.Println("[=]")
}

// Helper function to create a test request with mux vars
func newRequestWithVars(method, path string, vars map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return mux.SetURLVars(req, vars)
}
