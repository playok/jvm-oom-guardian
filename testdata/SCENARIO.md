# OOM 재시작 샘플 시나리오

이 시나리오는 32MiB heap의 임시 Java 프로세스를 OOM 상태로 만들고, Go 데몬이 새 Java 프로세스를 시작하는 전체 흐름을 확인합니다. 설정 파일의 프로젝트 절대 경로는 실행 환경에 맞게 수정하십시오.

```sh
mkdir -p /tmp/jvm-oom-guardian-scenario-classes
javac -d /tmp/jvm-oom-guardian-scenario-classes testdata/OomTestDaemon.java
./scripts/build.sh
./bin/jvm-oom-guardian server --config testdata/scenario-config.json
```

별도 터미널에서 OOM을 발생시킵니다.

```sh
/usr/bin/java \
  -Xms16m -Xmx32m \
  -XX:+HeapDumpOnOutOfMemoryError \
  -XX:HeapDumpPath=/tmp/jvm-oom-guardian-scenario.hprof \
  -XX:+CrashOnOutOfMemoryError \
  "-XX:OnOutOfMemoryError=/absolute/path/to/bin/jvm-oom-guardian notify --service sample-tomcat --pid %p --socket /tmp/jvm-oom-guardian-scenario.sock" \
  -cp /tmp/jvm-oom-guardian-scenario-classes OomTestDaemon oom \
  /tmp/jvm-oom-guardian-scenario.pid /tmp/jvm-oom-guardian-scenario.ready
```

성공 시 다음을 확인할 수 있습니다.

- `/tmp/jvm-oom-guardian-scenario.hprof` heap dump
- `hs_err_pid*.log` JVM fatal error log
- `/tmp/jvm-oom-guardian-scenario.ready` 새 JVM PID
- `/tmp/jvm-oom-guardian-scenario-logs/daemon-YYYYMMDD.log` JSON 데몬 로그
- `/tmp/jvm-oom-guardian-scenario-logs/tomcat-start.log` 새 JVM stdout 로그
