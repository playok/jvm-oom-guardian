# jvm-oom-guardian

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

## Configuration and logs

Start with [`config.example.json`](config.example.json). It defines the socket, PID and daemon log files, service commands, rolling log directory, filename pattern, retention period, and maximum file count. Logs are date-rolled and old files are cleaned automatically.

## Testing and release

```bash
./scripts/test.sh
./scripts/release.sh v1.0.0
```

The reproducible OOM scenario is documented in [`testdata/SCENARIO.md`](testdata/SCENARIO.md). Run the daemon as the service user and verify command paths and permissions before production deployment.
