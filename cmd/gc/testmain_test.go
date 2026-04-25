package main

import "os"

// scrubInheritedRigEnv unsets gc rig environment variables that would otherwise
// leak from a parent gc agent session into go test. With GC_BEADS=bd inherited
// from the rig, file-provider tests in this package were silently engaging the
// managed-dolt lifecycle and orphaning dolt sql-server processes (ga-d02c).
// Tests that legitimately need these env vars must set them with t.Setenv.
//
// Called from TestMain in main_test.go before any test runs.
func scrubInheritedRigEnv() {
	for _, key := range []string{
		"GC_BEADS",
		"GC_BEADS_SCOPE_ROOT",
		"GC_BEADS_PREFIX",
		"GC_DOLT",
		"GC_DOLT_HOST",
		"GC_DOLT_PORT",
		"GC_DOLT_USER",
		"GC_DOLT_PASSWORD",
		"GC_DOLT_DATA_DIR",
		"GC_DOLT_LOG_FILE",
		"GC_DOLT_STATE_FILE",
		"GC_DOLT_PID_FILE",
		"GC_DOLT_LOCK_FILE",
		"GC_DOLT_CONFIG_FILE",
		"GC_CITY_RUNTIME_DIR",
		"GC_PACK_STATE_DIR",
		"BEADS_DOLT_SERVER_HOST",
		"BEADS_DOLT_SERVER_PORT",
		"BEADS_DOLT_SERVER_USER",
		"BEADS_DOLT_SERVER_PASSWORD",
		"BEADS_DOLT_SERVER_DATABASE",
		"BEADS_DOLT_AUTO_START",
		"BEADS_CREDENTIALS_FILE",
		"BEADS_DIR",
		"BEADS_ACTOR",
	} {
		_ = os.Unsetenv(key)
	}
}
