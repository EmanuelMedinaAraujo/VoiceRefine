package main

// version is when the commit the exe was built from was made, in UTC, and the commit:
// "20261027.231100-e68249f". build.sh sets it; a plain go build leaves it empty.
var version string

// olderThan is true when this exe was built from an older commit than the installed one, so
// installing it would go back. A build without a version, or an install from before versions,
// never counts as newer.
func olderThan(installed string) bool {
	return version != "" && installed != "" && commitTime(installed) > commitTime(version)
}

// commitTime is the part of a version that orders it: the fixed-width time before the commit.
func commitTime(v string) string {
	if len(v) < len("20261027.231100") {
		return ""
	}
	return v[:len("20261027.231100")]
}
