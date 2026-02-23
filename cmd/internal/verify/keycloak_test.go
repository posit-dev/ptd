package verify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetKeycloakAdminToken_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	_, err := getKeycloakAdminToken(context.Background(), srv.URL, "admin", "wrongpass")
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
}

func TestGetKeycloakAdminToken_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	_, err := getKeycloakAdminToken(context.Background(), srv.URL, "admin", "pass")
	if err == nil {
		t.Fatal("expected error for invalid JSON response, got nil")
	}
}

func TestGetKeycloakAdminToken_EmptyAccessToken(t *testing.T) {
	// A 200 response with valid JSON but no access_token field should return an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	_, err := getKeycloakAdminToken(context.Background(), srv.URL, "admin", "pass")
	if err == nil {
		t.Fatal("expected error when access_token is absent from response, got nil")
	}
}

func TestCreateKeycloakUser_CreateFails(t *testing.T) {
	// Search returns empty list, create returns 409, re-search also returns empty list → error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"errorMessage":"User exists with same username"}`))
		}
	}))
	defer srv.Close()

	err := createKeycloakUser(context.Background(), srv.URL, "myrealm", "token", "user", "pass")
	if err == nil {
		t.Fatal("expected error when re-search after 409 finds no user, got nil")
	}
}

func TestCreateKeycloakUser_409ResearchResetsPassword(t *testing.T) {
	// Search fails with 403, create returns 409, re-search finds the user → reset password.
	userID := "found-user-id"
	getCount := 0
	resetCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCount++
			if getCount == 1 {
				// First search: permission denied
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(`{"error":"access_denied"}`))
			} else {
				// Re-search after 409: user found
				users := []map[string]interface{}{{"id": userID, "username": "user"}}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(users)
			}
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"errorMessage":"User exists with same username"}`))
		case http.MethodPut:
			resetCalled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	err := createKeycloakUser(context.Background(), srv.URL, "myrealm", "token", "user", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resetCalled {
		t.Error("expected password reset to be called after 409 re-search")
	}
}

func TestCreateKeycloakUser_SearchReturnsError(t *testing.T) {
	// Search returns 403 Forbidden (insufficient permissions)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer srv.Close()

	// Non-200 search falls through to create attempt; create also gets 403.
	// Either way an error should be returned.
	err := createKeycloakUser(context.Background(), srv.URL, "myrealm", "token", "user", "pass")
	if err == nil {
		t.Fatal("expected error when create returns 403, got nil")
	}
}

func TestResetKeycloakUserPassword_NonNoContentStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errorMessage":"Invalid password policy"}`))
	}))
	defer srv.Close()

	client := &http.Client{}
	err := resetKeycloakUserPassword(context.Background(), srv.URL, "myrealm", "token", "user-id", "newpass", client)
	if err == nil {
		t.Fatal("expected error for 400 reset response, got nil")
	}
}

func TestCreateKeycloakUser_ExistingUserResetsPassword(t *testing.T) {
	userID := "abc-123"
	searchCalled := false
	resetCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			searchCalled = true
			w.WriteHeader(http.StatusOK)
			users := []map[string]interface{}{{"id": userID, "username": "existing-user"}}
			json.NewEncoder(w).Encode(users)
		case http.MethodPut:
			resetCalled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	err := createKeycloakUser(context.Background(), srv.URL, "myrealm", "token", "existing-user", "newpass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !searchCalled {
		t.Error("expected search to be called")
	}
	if !resetCalled {
		t.Error("expected password reset to be called for existing user")
	}
}
