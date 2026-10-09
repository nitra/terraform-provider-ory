// Package empirical documents, as executable checks, how Ory Hydra v26.2.0
// actually behaves in the areas the provider design depends on. The tests talk
// to the local Hydra from compose.yaml directly (no provider involved) and run
// only with TF_ACC=1 (or HYDRA_EMPIRICAL=1):
//
//	podman compose up -d --wait
//	HYDRA_EMPIRICAL=1 go test ./internal/empirical -v -count=1
//	podman compose down -v
//
// Each test asserts the behaviour observed on v26.2.0, so a Hydra upgrade that
// changes it makes the test fail loudly.
package empirical

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/nitra/terraform-provider-ory/internal/testhydra"
)

func setup(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" && os.Getenv("HYDRA_EMPIRICAL") == "" {
		t.Skip("set TF_ACC=1 or HYDRA_EMPIRICAL=1 to run against the local Hydra")
	}
	testhydra.Ready(t)
}

func uniq(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()) }

func createClient(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	st, b := testhydra.Admin(t, http.MethodPost, "/admin/clients", body)
	if st != http.StatusCreated {
		t.Fatalf("create client: %d %s", st, b)
	}
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	t.Cleanup(func() { testhydra.Admin(t, http.MethodDelete, "/admin/clients/"+body["client_id"].(string), nil) })
	return out
}

func trustCleanup(t *testing.T, id string) {
	t.Cleanup(func() { testhydra.Admin(t, http.MethodDelete, "/admin/trust/grants/jwt-bearer/issuers/"+id, nil) })
}

// (1) Is a trust's expires_at enforced when the assertion carries a `kid`?
//
// Hydra itself does not reject an expires_at in the past (only the provider
// validates it), so an already-expired trust can be created honestly through
// the Admin API - no clock tricks. A second trust is created with a 3s expiry
// and used after it has elapsed, to rule out any create-time special-casing.
func TestEmpirical1_TrustExpiryWithKid(t *testing.T) {
	setup(t)
	clientID, secret := uniq("e1-client"), "e1-secret-123456"
	createClient(t, map[string]any{
		"client_id": clientID, "client_secret": secret,
		"grant_types": []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "scope": "apicurio:developer",
	})

	// (a) created already expired
	iss, sub := "https://"+uniq("e1a")+".example", "repo:7n-45/rules-132:ref:refs/heads/main"
	key := testhydra.NewKey(t, "e1a-kid")
	id, st, b := testhydra.CreateTrust(t, iss, sub, []string{"apicurio:developer"}, key.PublicJWK(t), time.Now().Add(-1*time.Hour))
	if st != http.StatusCreated {
		t.Fatalf("Hydra rejected a trust with expires_at in the past: %d %s", st, b)
	}
	trustCleanup(t, id)
	t.Logf("(1a) Hydra accepted a trust with expires_at = now-1h (no server-side validation)")

	withKid := testhydra.JWTBearer(t, clientID, secret, key.Assertion(t, iss, sub, 30*time.Minute, true), "apicurio:developer")
	noKid := testhydra.JWTBearer(t, clientID, secret, key.Assertion(t, iss, sub, 30*time.Minute, false), "apicurio:developer")
	t.Logf("(1a) expired trust, assertion WITH kid:    %s", withKid)
	t.Logf("(1a) expired trust, assertion WITHOUT kid: %s", noKid)

	// (b) expires 3s after creation, used after expiry
	iss2 := "https://" + uniq("e1b") + ".example"
	key2 := testhydra.NewKey(t, "e1b-kid")
	id2, st2, b2 := testhydra.CreateTrust(t, iss2, sub, []string{"apicurio:developer"}, key2.PublicJWK(t), time.Now().Add(3*time.Second))
	if st2 != http.StatusCreated {
		t.Fatalf("create short-lived trust: %d %s", st2, b2)
	}
	trustCleanup(t, id2)
	before := testhydra.JWTBearer(t, clientID, secret, key2.Assertion(t, iss2, sub, 30*time.Minute, true), "apicurio:developer")
	time.Sleep(6 * time.Second)
	afterKid := testhydra.JWTBearer(t, clientID, secret, key2.Assertion(t, iss2, sub, 30*time.Minute, true), "apicurio:developer")
	afterNoKid := testhydra.JWTBearer(t, clientID, secret, key2.Assertion(t, iss2, sub, 30*time.Minute, false), "apicurio:developer")
	t.Logf("(1b) before expiry, WITH kid:           %s", before)
	t.Logf("(1b) after expiry,  WITH kid:           %s", afterKid)
	t.Logf("(1b) after expiry,  WITHOUT kid:        %s", afterNoKid)

	if !before.OK() {
		t.Fatalf("sanity: valid trust must work: %s", before)
	}
	// Observed on v26.2.0: expiry is NOT enforced when the assertion has a kid
	// (GetPublicKey/GetPublicKeyScopes ignore expires_at), but IS enforced
	// without kid (GetPublicKeys filters expires_at > now).
	if !withKid.OK() || !afterKid.OK() {
		t.Errorf("expected expired trust to still be accepted WITH kid on v26.2.0 (behaviour changed?): a=%s b=%s", withKid, afterKid)
	}
	if noKid.OK() || afterNoKid.OK() {
		t.Errorf("expected expired trust to be rejected WITHOUT kid: a=%s b=%s", noKid, afterNoKid)
	}
}

// (2) Does deleting one trust delete the JWK (issuer, kid) shared with another
// trust of the same issuer and key but a different subject?
func TestEmpirical2_DeleteTrustSharedJWK(t *testing.T) {
	setup(t)
	clientID, secret := uniq("e2-client"), "e2-secret-123456"
	createClient(t, map[string]any{
		"client_id": clientID, "client_secret": secret,
		"grant_types": []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "scope": "apicurio:developer",
	})
	iss := "https://" + uniq("e2") + ".example"
	key := testhydra.NewKey(t, "e2-shared-kid")
	exp := time.Now().Add(24 * time.Hour)
	idA, st, b := testhydra.CreateTrust(t, iss, "subject-a", []string{"apicurio:developer"}, key.PublicJWK(t), exp)
	if st != http.StatusCreated {
		t.Fatalf("trust A: %d %s", st, b)
	}
	idB, st, b := testhydra.CreateTrust(t, iss, "subject-b", []string{"apicurio:developer"}, key.PublicJWK(t), exp)
	if st != http.StatusCreated {
		t.Fatalf("trust B: %d %s", st, b)
	}
	trustCleanup(t, idB)

	// duplicate (issuer, subject, kid) - relevant for create_before_destroy
	_, stDup, bDup := testhydra.CreateTrust(t, iss, "subject-a", []string{"apicurio:developer"}, key.PublicJWK(t), exp.Add(time.Hour))
	t.Logf("(2) second trust with same issuer+subject+kid: %d %s", stDup, bDup)

	beforeB := testhydra.JWTBearer(t, clientID, secret, key.Assertion(t, iss, "subject-b", 30*time.Minute, true), "apicurio:developer")
	if st, b := testhydra.Admin(t, http.MethodDelete, "/admin/trust/grants/jwt-bearer/issuers/"+idA, nil); st != http.StatusNoContent {
		t.Fatalf("delete A: %d %s", st, b)
	}
	stGetB, _ := testhydra.Admin(t, http.MethodGet, "/admin/trust/grants/jwt-bearer/issuers/"+idB, nil)
	afterB := testhydra.JWTBearer(t, clientID, secret, key.Assertion(t, iss, "subject-b", 30*time.Minute, true), "apicurio:developer")
	t.Logf("(2) trust B before deleting A: %s", beforeB)
	t.Logf("(2) after deleting A: GET trust B -> %d; token for B: %s", stGetB, afterB)

	if !beforeB.OK() {
		t.Fatalf("sanity: trust B must work before: %s", beforeB)
	}
	if stDup == http.StatusCreated {
		t.Errorf("expected a duplicate issuer+subject+kid trust to be rejected (unique index)")
	}
	// Observed on v26.2.0: DeleteGrant also deletes the JWK (set=issuer, kid),
	// and the FK hydra_oauth2_trusted_jwt_bearer_issuer(key_set, key_id) ->
	// hydra_jwk ON DELETE CASCADE silently deletes trust B as well.
	if stGetB != http.StatusNotFound || afterB.OK() {
		t.Errorf("expected trust B to be cascade-deleted with the shared JWK: GET=%d token=%s", stGetB, afterB)
	}
}

// (3) Does PUT /admin/clients/{id} without client_secret keep the secret?
// (4) Are lifespans in the create body applied?
func TestEmpirical3and4_PutKeepsSecret_CreateLifespans(t *testing.T) {
	setup(t)
	clientID, secret := uniq("e34"), "e34-secret-123456"
	created := createClient(t, map[string]any{
		"client_id": clientID, "client_secret": secret, "client_name": "before",
		"grant_types": []string{"client_credentials"}, "scope": "a",
		"client_credentials_grant_access_token_lifespan": "7m",
		"jwt_bearer_grant_access_token_lifespan":         "15m",
	})
	t.Logf("(4) create response lifespans: client_credentials=%v jwt_bearer=%v",
		created["client_credentials_grant_access_token_lifespan"], created["jwt_bearer_grant_access_token_lifespan"])
	_, gb := testhydra.Admin(t, http.MethodGet, "/admin/clients/"+clientID, nil)
	var got map[string]any
	_ = json.Unmarshal(gb, &got)
	tok := testhydra.ClientCredentials(t, clientID, secret, "a")
	t.Logf("(4) GET lifespans: client_credentials=%v jwt_bearer=%v; token: %s",
		got["client_credentials_grant_access_token_lifespan"], got["jwt_bearer_grant_access_token_lifespan"], tok)
	if got["client_credentials_grant_access_token_lifespan"] != "7m0s" || got["jwt_bearer_grant_access_token_lifespan"] != "15m0s" {
		t.Errorf("(4) create-body lifespans were not applied: %v", got)
	}
	if !tok.OK() || tok.ExpiresIn < 400 || tok.ExpiresIn > 420 {
		t.Errorf("(4) expected a ~420s token, got %s", tok)
	}

	// (3) full PUT without client_secret
	put := map[string]any{
		"client_id": clientID, "client_name": "after",
		"grant_types": []string{"client_credentials"}, "scope": "a",
	}
	st, pb := testhydra.Admin(t, http.MethodPut, "/admin/clients/"+clientID, put)
	if st != http.StatusOK {
		t.Fatalf("PUT: %d %s", st, pb)
	}
	var putResp map[string]any
	_ = json.Unmarshal(pb, &putResp)
	afterPut := testhydra.ClientCredentials(t, clientID, secret, "a")
	t.Logf("(3) PUT without client_secret: response has client_secret=%v; old secret -> %s; lifespan after PUT=%v",
		putResp["client_secret"] != nil, afterPut, putResp["client_credentials_grant_access_token_lifespan"])
	if !afterPut.OK() {
		t.Errorf("(3) secret lost after PUT without client_secret: %s", afterPut)
	}
	// PUT is a full replacement: omitted lifespans are reset.
	if putResp["client_credentials_grant_access_token_lifespan"] != nil {
		t.Errorf("(3) expected omitted lifespan to be reset by PUT, got %v", putResp["client_credentials_grant_access_token_lifespan"])
	}

	// PUT with a new secret rotates it
	put["client_secret"] = "e34-secret-rotated"
	if st, pb := testhydra.Admin(t, http.MethodPut, "/admin/clients/"+clientID, put); st != http.StatusOK {
		t.Fatalf("PUT rotate: %d %s", st, pb)
	}
	oldS := testhydra.ClientCredentials(t, clientID, secret, "a")
	newS := testhydra.ClientCredentials(t, clientID, "e34-secret-rotated", "a")
	t.Logf("(3) after PUT with new secret: old -> %s; new -> %s", oldS, newS)
	if oldS.OK() || !newS.OK() {
		t.Errorf("(3) rotation via PUT did not behave: old=%s new=%s", oldS, newS)
	}
}

// (5) Can a PUBLIC client (token_endpoint_auth_method=none) use the
// jwt-bearer grant? Assertion: own RSA key as "issuer", JWT with kid,
// aud = token URL, no jti (jti_optional: true), max_ttl 70m.
func TestEmpirical5_PublicClientJWTBearer(t *testing.T) {
	setup(t)
	clientID := uniq("e5-public")
	createClient(t, map[string]any{
		"client_id": clientID, "token_endpoint_auth_method": "none",
		"grant_types":                            []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"scope":                                  "apicurio:developer",
		"jwt_bearer_grant_access_token_lifespan": "10m",
	})
	iss := "https://" + uniq("e5") + ".example"
	sub := "repo:7n-45/rules-132:ref:refs/heads/main"
	key := testhydra.NewKey(t, "e5-kid")
	id, st, b := testhydra.CreateTrust(t, iss, sub, []string{"apicurio:developer"}, key.PublicJWK(t), time.Now().Add(10*365*24*time.Hour))
	if st != http.StatusCreated {
		t.Fatalf("trust: %d %s", st, b)
	}
	trustCleanup(t, id)

	ok := testhydra.JWTBearer(t, clientID, "", key.Assertion(t, iss, sub, 60*time.Minute, true), "apicurio:developer")
	noClientID := testhydra.Token(t, map[string][]string{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {key.Assertion(t, iss, sub, 60*time.Minute, true)},
		"scope":      {"apicurio:developer"},
	}, "", "")
	tooLong := testhydra.JWTBearer(t, clientID, "", key.Assertion(t, iss, sub, 80*time.Minute, true), "apicurio:developer")
	wrongSub := testhydra.JWTBearer(t, clientID, "", key.Assertion(t, iss, "repo:other:ref:refs/heads/main", 60*time.Minute, true), "apicurio:developer")
	wrongScope := testhydra.JWTBearer(t, clientID, "", key.Assertion(t, iss, sub, 60*time.Minute, true), "apicurio:admin")
	t.Logf("(5) public client + client_id, ttl 60m, no jti: %s", ok)
	t.Logf("(5) no client_id at all:                      %s", noClientID)
	t.Logf("(5) ttl 80m (> max_ttl 70m):                   %s", tooLong)
	t.Logf("(5) other subject:                             %s", wrongSub)
	t.Logf("(5) scope not in trust:                        %s", wrongScope)

	if !ok.OK() {
		t.Errorf("(5) public client could not use jwt-bearer: %s", ok)
	}
	if ok.OK() && (ok.ExpiresIn < 580 || ok.ExpiresIn > 600) {
		t.Errorf("(5) expected the 10m jwt_bearer lifespan to apply, got expires_in=%d", ok.ExpiresIn)
	}
	if noClientID.OK() {
		t.Errorf("(5) expected a request without any client authentication to fail (CanSkipClientAuth=false)")
	}
	if tooLong.OK() || wrongSub.OK() || wrongScope.OK() {
		t.Errorf("(5) negative cases unexpectedly succeeded: ttl=%s sub=%s scope=%s", tooLong, wrongSub, wrongScope)
	}
}
