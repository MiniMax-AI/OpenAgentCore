package main

import "testing"

func TestInstallationFactsReportThePlainHTTPOptIn(t *testing.T) {
	facts, err := installationFacts("http://10.0.0.5:8091", true)
	if err != nil || facts.PublicURL == nil || *facts.PublicURL != "http://10.0.0.5:8091" || facts.LocalOnly || !facts.InsecurePublicURL {
		t.Fatalf("opted-in origin: %+v %v", facts, err)
	}
	facts, err = installationFacts("https://core.example", false)
	if err != nil || facts.InsecurePublicURL || facts.LocalOnly {
		t.Fatalf("default origin: %+v %v", facts, err)
	}
	facts, err = installationFacts("", false)
	if err != nil || facts.PublicURL != nil || facts.APIBaseURL != nil || facts.InsecurePublicURL {
		t.Fatalf("unset origin: %+v %v", facts, err)
	}
}
