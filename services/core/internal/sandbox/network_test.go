package sandbox

import "testing"

func TestNetworkIsValidatedAgainstTheProviderDeclaration(t *testing.T) {
	none, closed, full := NetworkSupport{}, NetworkSupport{Restrict: true}, NetworkSupport{Restrict: true, PrivateDomains: true}
	for _, tc := range []struct {
		name    string
		network Network
		support NetworkSupport
		param   string
	}{
		{"default", DefaultNetwork(), none, ""},
		{"closed", Network{Domains: []string{}}, closed, ""},
		{"closed without support", Network{Domains: []string{}}, none, "network.public"},
		{"domains", Network{Public: true, Domains: []string{"gitlab.example.com"}}, full, ""},
		{"domains without support", Network{Public: true, Domains: []string{"gitlab.example.com"}}, closed, "network.domains"},
		{"wildcard", Network{Public: true, Domains: []string{"*.example.com"}}, full, "network.domains"},
		{"address", Network{Public: true, Domains: []string{"10.0.0.1"}}, full, "network.domains"},
	} {
		err := tc.network.ValidatePolicy("test", tc.support)
		var invalid *ValidationError
		if tc.param == "" && err != nil || tc.param != "" && (!asValidation(err, &invalid) || invalid.Param != tc.param) {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	got := Network{Domains: []string{"B.example.com ", "a.example.com", "b.example.com"}}.Canonical().Domains
	if len(got) != 2 || got[0] != "a.example.com" || got[1] != "b.example.com" {
		t.Fatalf("canonical domains: %v", got)
	}
}

func asValidation(err error, target **ValidationError) bool {
	v, ok := err.(*ValidationError)
	*target = v
	return ok
}
