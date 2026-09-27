package log

import "os"

// RenamedEnvironment lists retired logging settings that are present, including
// empty values. It never includes setting values in diagnostics.
func RenamedEnvironment() []string {
	var matches []string
	for _, suffix := range []string{"LEVEL", "FORMAT", "ADD_SOURCE"} {
		old := "PARSAR_LOG_" + suffix
		if _, present := os.LookupEnv(old); present {
			matches = append(matches, old+" → OAC_LOG_"+suffix)
		}
	}
	return matches
}
