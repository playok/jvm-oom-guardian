# jvm-oom-guardian

[![CI](https://github.com/playok/jvm-oom-guardian/actions/workflows/ci.yml/badge.svg)](https://github.com/playok/jvm-oom-guardian/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/release/playok/jvm-oom-guardian)](https://github.com/playok/jvm-oom-guardian/releases) [![License](https://img.shields.io/github/license/playok/jvm-oom-guardian)](LICENSE)

[한국어 문서](README_kr.md)

`jvm-oom-guardian` is a small, non-root-friendly supervisor for Java services. It receives `OutOfMemoryError` notifications over a Unix domain socket, terminates the failed JVM, and runs the configured Tomcat restart command.

## Build and run

```bash
./scripts/build.sh
cp config.example.json ~/.jvm_oom_guardian.json
./bin/jvm-oom-guardian server start --config ~/.jvm_oom_guardian.json
./bin/jvm-oom-guardian server status --config ~/.jvm_oom_guardian.json
```

Stop the daemon with `server stop`; it removes the PID file and socket after shutdown. `server run` is available for foreground operation and diagnostics. Use `--help` on any command for the full option list.

## JVM integration

```text
-XX:+HeapDumpOnOutOfMemoryError
-XX:HeapDumpPath=/var/lib/tomcat/oom
-XX:OnOutOfMemoryError='/usr/local/bin/jvm-oom-guardian notify --service my-tomcat --pid %p'
```

The client sends a JSON event containing the service name, JVM PID, timestamp, and a random event ID to `~/.jvm_oom_guardian.sock` by default. The daemon validates PID ownership, stops the failed process, and executes the configured `start_command`.

### Apache Tomcat `setenv.sh` example

Create `$CATALINA_BASE/bin/setenv.sh` and make sure the Tomcat user can write the heap-dump directory and access the guardian socket:

```bash
#!/usr/bin/env bash
set -euo pipefail

OOM_DIR="/var/lib/tomcat/oom"
mkdir -p "$OOM_DIR"

export CATALINA_OPTS="${CATALINA_OPTS:-} \\
  -XX:+HeapDumpOnOutOfMemoryError \\
  -XX:HeapDumpPath=$OOM_DIR \\
  -XX:OnOutOfMemoryError='/usr/local/bin/jvm-oom-guardian notify --service my-tomcat --pid %p'"
```

The daemon must already be running as the same user before Tomcat starts. If a non-default socket is used, add `--socket /path/to/.jvm_oom_guardian.sock` to the notification command. Keep `%p` unchanged; the JVM substitutes it with the failing process ID.

## Configuration and logs

Start with [`config.example.json`](config.example.json). It defines the socket, PID and daemon log files, service commands, rolling log directory, filename pattern, retention period, and maximum file count. Logs are date-rolled and old files are cleaned automatically.

## Testing and release

```bash
./scripts/test.sh
goreleaser release --clean
```

Install [GoReleaser](https://goreleaser.com/) and create a Git tag before running a release. GoReleaser produces Linux, macOS, and Windows archives for amd64 (plus arm64 on Linux and macOS), including the configuration and documentation files. Use `make release-snapshot` to build locally without publishing.

Pushing a semantic-version tag automatically publishes a GitHub Release with the generated archives and `checksums.txt`:

```bash
git tag v1.0.0
git push origin v1.0.0
```

The reproducible OOM scenario is documented in [`testdata/SCENARIO.md`](testdata/SCENARIO.md). Run the daemon as the service user and verify command paths and permissions before production deployment.
