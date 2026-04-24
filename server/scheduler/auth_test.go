package scheduler

import "testing"

type fakeAuth struct {
	want map[string]string
	gotKey, gotPwd string
}

func (f *fakeAuth) Valid(user, pwd, _ string) bool {
	f.gotKey = user
	f.gotPwd = pwd
	want, ok := f.want[user]
	return ok && want == pwd
}

func TestUsernameCredentialStore_DelegatesByAuthKey(t *testing.T) {
	inner := &fakeAuth{want: map[string]string{"B_38313:Ab000001": "secret"}}
	store := &UsernameCredentialStore{Inner: inner}

	if !store.Valid("B_38313_US_5_Ab000001", "secret", "") {
		t.Fatal("should accept correct credentials")
	}
	if inner.gotKey != "B_38313:Ab000001" {
		t.Fatalf("delegation key=%q", inner.gotKey)
	}
	// Country / duration changes must NOT change the auth result.
	if !store.Valid("B_38313_DE_30_Ab000001", "secret", "") {
		t.Fatal("country/duration should not affect auth")
	}
}

func TestUsernameCredentialStore_RejectsBad(t *testing.T) {
	store := &UsernameCredentialStore{Inner: &fakeAuth{want: map[string]string{"k": "p"}}}
	cases := []struct {
		u, p string
	}{
		{"", "p"},
		{"not-a-username", "p"},
		{"B_1_US_5_Ab000001", ""},
	}
	for _, c := range cases {
		if store.Valid(c.u, c.p, "") {
			t.Errorf("should reject u=%q p=%q", c.u, c.p)
		}
	}
}
