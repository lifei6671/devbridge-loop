package app

import "testing"

func TestBuildManagedCALogDetails(testingObject *testing.T) {
	testingObject.Parallel()

	logDetails := buildManagedCALogDetails(
		" /home/test/.config/devbridge/root-ca.crt ",
		"/home/test/.config/devbridge/root-ca.key",
	)

	if logDetails.caCertDirectory != "/home/test/.config/devbridge" {
		testingObject.Fatalf("unexpected caCertDirectory: got=%q", logDetails.caCertDirectory)
	}
	if logDetails.caCertFile != "/home/test/.config/devbridge/root-ca.crt" {
		testingObject.Fatalf("unexpected caCertFile: got=%q", logDetails.caCertFile)
	}
	if logDetails.caKeyDirectory != "/home/test/.config/devbridge" {
		testingObject.Fatalf("unexpected caKeyDirectory: got=%q", logDetails.caKeyDirectory)
	}
	if logDetails.caKeyFile != "/home/test/.config/devbridge/root-ca.key" {
		testingObject.Fatalf("unexpected caKeyFile: got=%q", logDetails.caKeyFile)
	}
}

func TestBuildManagedCALogDetailsAllowsEmptyPath(testingObject *testing.T) {
	testingObject.Parallel()

	logDetails := buildManagedCALogDetails("", " ")

	if logDetails.caCertDirectory != "" || logDetails.caCertFile != "" {
		testingObject.Fatalf("expected empty cert details, got=%+v", logDetails)
	}
	if logDetails.caKeyDirectory != "" || logDetails.caKeyFile != "" {
		testingObject.Fatalf("expected empty key details, got=%+v", logDetails)
	}
}
