package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultSocketName = ".jvm_oom_guardian.sock"
	maxMessageSize    = 4096
)

type config struct {
	Socket   string                   `json:"socket,omitempty"`
	Services map[string]serviceConfig `json:"services"`
	Logging  loggingConfig            `json:"logging,omitempty"`
	Daemon   daemonConfig             `json:"daemon,omitempty"`
}

type daemonConfig struct {
	PIDFile string `json:"pid_file,omitempty"`
	LogFile string `json:"log_file,omitempty"`
}

type loggingConfig struct {
	Directory     string `json:"directory,omitempty"`
	FilePattern   string `json:"file_pattern,omitempty"`
	Format        string `json:"format,omitempty"`
	Rotate        *bool  `json:"rotate,omitempty"`
	MaxBytes      int64  `json:"max_bytes,omitempty"`
	MaxFiles      int    `json:"max_files,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

type serviceConfig struct {
	PIDFile         string            `json:"pid_file"`
	ProcessMatch    string            `json:"process_match"`
	StartCommand    []string          `json:"start_command"`
	WorkDir         string            `json:"work_dir,omitempty"`
	Environment     map[string]string `json:"environment,omitempty"`
	ExitWait        duration          `json:"exit_wait,omitempty"`
	StopTimeout     duration          `json:"stop_timeout,omitempty"`
	RestartDelay    duration          `json:"restart_delay,omitempty"`
	StartTimeout    duration          `json:"start_timeout,omitempty"`
	VerifyTimeout   duration          `json:"verify_timeout,omitempty"`
	RestartLog      string            `json:"restart_log,omitempty"`
	RestartLogMax   int64             `json:"restart_log_max_bytes,omitempty"`
	RestartLogFiles int               `json:"restart_log_max_files,omitempty"`
	RestartLogDays  int               `json:"restart_log_retention_days,omitempty"`
	RestartLimit    int               `json:"restart_limit,omitempty"`
	RestartWindow   duration          `json:"restart_window,omitempty"`
	BackoffInitial  duration          `json:"backoff_initial,omitempty"`
	BackoffMax      duration          `json:"backoff_max,omitempty"`
}

type duration struct{ time.Duration }

func (d *duration) UnmarshalJSON(b []byte) error {
	var value string
	if err := json.Unmarshal(b, &value); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

type request struct {
	Service string `json:"service"`
	PID     int    `json:"pid"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "server":
		err = serverMain(os.Args[2:])
	case "notify":
		err = notifyMain(os.Args[2:])
	case "help", "-h", "--help":
		usage(os.Stdout)
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Printf("ERROR: %v", err)
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	_, _ = io.WriteString(w, `jvm-oom-guardian

JVM OutOfMemoryError 알림을 Unix domain socket으로 받아 해당 Tomcat을
안전하게 종료하고 다시 시작하는 로컬 데몬입니다. 서버와 알림 클라이언트가
하나의 바이너리에 포함되어 있습니다.

사용법:
  jvm-oom-guardian server [--config PATH]
  jvm-oom-guardian notify --service NAME --pid PID [--socket PATH]

명령:
  server   Unix socket 데몬을 포그라운드에서 실행합니다.
  notify   OOM JVM의 PID를 데몬에 전달하고 접수 응답을 기다립니다.
  help     이 도움말을 출력합니다.

빠른 시작:
  1. config.example.json을 ~/.jvm_oom_guardian.json으로 복사합니다.
  2. pid_file, process_match, start_command를 실제 Tomcat에 맞게 수정합니다.
  3. 설정 파일 권한을 600으로 지정합니다.
  4. Tomcat과 같은 OS 사용자로 server를 실행합니다.

  jvm-oom-guardian server \
    --config ~/.jvm_oom_guardian.json

권장 JVM 옵션:
  -XX:+HeapDumpOnOutOfMemoryError
  -XX:HeapDumpPath=/opt/tomcat/dumps
  -XX:+CrashOnOutOfMemoryError
  -XX:OnOutOfMemoryError='/usr/local/bin/jvm-oom-guardian notify --service my-tomcat --pid %p'

핵심 안전 장치:
  * 전달 PID, PID 파일, process_match가 모두 일치할 때만 종료합니다.
  * JVM의 heap/core dump와 자체 종료를 먼저 기다립니다.
  * 대기 후에도 살아 있으면 SIGTERM, 이어서 필요할 때 SIGKILL을 보냅니다.
  * 서비스별 중복 재시작과 Linux PID 재사용 사고를 방지합니다.
  * 소켓은 기본 0600이며 설정 파일의 group/other 쓰기 권한을 거부합니다.

주의:
  * 기본 소켓은 ~/.jvm_oom_guardian.sock 입니다.
  * pid_file은 Tomcat의 CATALINA_PID와 같은 경로여야 합니다.
  * process_match에는 Tomcat별 고유한 -Dcatalina.base=... 사용을 권장합니다.
  * 큰 heap/core dump에는 exit_wait 기본값 30초가 부족할 수 있습니다.
  * native core 생성에는 OS core 설정과 충분한 디스크 공간이 필요합니다.
  * 데몬은 포그라운드로 실행하고 systemd 등의 서비스 관리자로 감시하십시오.

하위 명령 도움말:
  jvm-oom-guardian server --help
  jvm-oom-guardian notify --help

전체 설정과 운영 절차는 프로젝트 README.md를 참고하십시오.
`)
}

func serverUsage(w io.Writer, defaultConfig string) {
	fmt.Fprintf(w, `사용법:
  jvm-oom-guardian server start  [--config PATH]
  jvm-oom-guardian server stop   [--config PATH]
  jvm-oom-guardian server status [--config PATH]
  jvm-oom-guardian server run    [--config PATH]

Unix domain socket 데몬을 포그라운드에서 실행합니다. Tomcat과 같은 OS
사용자로 실행하고, 운영 환경에서는 systemd의 Restart=on-failure 사용을
권장합니다.

start는 사용자 권한으로 백그라운드 daemon을 시작하고 PID 파일을 기록합니다.
stop은 SIGTERM 후 필요하면 SIGKILL을 보내며, status는 PID와 명령행을 검증합니다.
run은 foreground 실행입니다.

옵션:
  --config PATH   JSON 설정 파일 (기본값: %s)

설정의 필수 필드:
  pid_file        CATALINA_PID와 같은 JVM PID 파일
  process_match   Java 명령행에서 확인할 고유 문자열
  start_command   Tomcat 시작 프로그램과 인자의 JSON 배열

주요 시간 설정:
  exit_wait       JVM 자체 종료/core 생성을 기다릴 시간 (기본 30s)
  stop_timeout    SIGTERM 후 기다릴 시간 (기본 10s)
  restart_delay   종료 후 재시작 전 대기 시간
  start_timeout   시작 명령 제한 시간 (기본 30s)
  verify_timeout  새 JVM PID 확인 제한 시간 (기본 30s)
  restart_limit   restart_window 안의 최대 재시작 횟수 (기본 5)
  restart_window  circuit breaker 관찰 구간 (기본 10m)
  backoff_initial 연속 OOM 재시작 초기 대기 (기본 5s)
  backoff_max     연속 OOM 재시작 최대 대기 (기본 5m)
  restart_log_max_bytes  restart_log 회전 크기 (기본 10MiB)

데몬 이벤트 로그:
  logging.directory / file_pattern / format / rotate / max_bytes /
  max_files / retention_days로 위치, 파일명, 포맷, rolling, 보관을 설정합니다.

예:
  jvm-oom-guardian server --config %s
`, defaultConfig, defaultConfig)
}

func notifyUsage(w io.Writer, defaultSocket string) {
	fmt.Fprintf(w, `사용법:
  jvm-oom-guardian notify --service NAME --pid PID [옵션]

OnOutOfMemoryError에서 호출되는 짧은 알림 클라이언트입니다. 종료와 재시작은
서버가 비동기로 처리하므로 클라이언트는 접수 응답 후 즉시 종료합니다.

필수 옵션:
  --service NAME  설정 파일의 서비스 이름
  --pid PID       OOM이 발생한 JVM PID; JVM 옵션에서는 %%p 사용

선택 옵션:
  --socket PATH   Unix socket (기본값: %s)
  --timeout TIME  연결 및 응답 제한 시간 (기본값: 3s)

JVM 옵션 예:
  -XX:OnOutOfMemoryError='/usr/local/bin/jvm-oom-guardian notify --service my-tomcat --pid %%p'

수동 실행은 PID 검증을 통과하면 실제 Tomcat을 종료하므로 테스트 인스턴스에서만
사용하십시오.
`, defaultSocket)
}

func serverMain(args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	action := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
	}
	if action != "run" && action != "start" && action != "stop" && action != "status" {
		return fmt.Errorf("unknown server action %q (expected start, stop, status, or run)", action)
	}
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	configPath := fs.String("config", filepath.Join(home, ".jvm_oom_guardian.json"), "JSON config path")
	fs.Usage = func() { serverUsage(fs.Output(), *configPath) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath, home)
	if err != nil {
		return err
	}
	applyDaemonDefaults(&cfg.Daemon, home)
	switch action {
	case "start":
		return startDaemon(*configPath, cfg.Daemon)
	case "stop":
		return stopDaemon(cfg.Daemon)
	case "status":
		return statusDaemon(cfg.Daemon)
	}
	eventLogger, err := configureLogging(cfg.Logging, home)
	if err != nil {
		return err
	}

	if err := prepareSocket(cfg.Socket); err != nil {
		return err
	}
	listener, err := net.Listen("unix", cfg.Socket)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Socket, err)
	}
	defer listener.Close()
	defer os.Remove(cfg.Socket)
	if err := os.Chmod(cfg.Socket, 0600); err != nil {
		return fmt.Errorf("secure socket permissions: %w", err)
	}
	log.Printf("listening on %s with %d configured service(s)", cfg.Socket, len(cfg.Services))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if eventLogger != nil {
		go eventLogger.runCleanup(ctx)
	}
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	h := &handler{
		cfg:       cfg,
		active:    make(map[string]bool),
		history:   make(map[string][]time.Time),
		connSlots: make(chan struct{}, 64),
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("shutdown complete")
				shutdown := time.NewTimer(2 * time.Minute)
				done := make(chan struct{})
				go func() { h.wg.Wait(); close(done) }()
				select {
				case <-done:
					log.Printf("in-flight operations completed")
				case <-shutdown.C:
					log.Printf("shutdown timeout; exiting with in-flight operation")
				}
				shutdown.Stop()
				return nil
			}
			log.Printf("accept error: %v", err)
			continue
		}
		select {
		case h.connSlots <- struct{}{}:
			h.wg.Add(1)
			go h.serveConn(context.Background(), conn)
		default:
			log.Printf("connection limit reached; rejecting client")
			_ = conn.Close()
		}
	}
}

func applyDaemonDefaults(cfg *daemonConfig, home string) {
	if cfg.PIDFile == "" {
		cfg.PIDFile = filepath.Join(home, ".jvm_oom_guardian.pid")
	} else {
		cfg.PIDFile = expandHome(cfg.PIDFile, home)
	}
	if cfg.LogFile == "" {
		cfg.LogFile = filepath.Join(home, ".jvm_oom_guardian", "server.log")
	} else {
		cfg.LogFile = expandHome(cfg.LogFile, home)
	}
}

func readDaemonPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("invalid daemon PID file %s", path)
	}
	return pid, nil
}

func daemonRunning(pid int) bool {
	if err := processExists(pid); err != nil {
		return false
	}
	command, err := processCommand(pid)
	return err == nil && strings.Contains(command, "jvm-oom-guardian") && strings.Contains(command, "server run")
}

func startDaemon(configPath string, daemon daemonConfig) error {
	if pid, err := readDaemonPID(daemon.PIDFile); err == nil {
		if daemonRunning(pid) {
			return fmt.Errorf("daemon is already running with PID %d", pid)
		}
		_ = os.Remove(daemon.PIDFile)
	}
	if err := os.MkdirAll(filepath.Dir(daemon.LogFile), 0700); err != nil {
		return fmt.Errorf("create daemon log directory: %w", err)
	}
	logFile, err := os.OpenFile(daemon.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()
	stdin, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open null stdin: %w", err)
	}
	defer stdin.Close()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	cmd := exec.Command(executable, "server", "run", "--config", configPath)
	// Detach from the invoking shell so server start survives the parent
	// process/terminal exiting. All standard streams are redirected below.
	detachCommand(cmd)
	cmd.Stdin = stdin
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if err := os.WriteFile(daemon.PIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0600); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("write daemon PID file: %w", err)
	}
	fmt.Printf("daemon started with PID %d\n", cmd.Process.Pid)
	return nil
}

func stopDaemon(daemon daemonConfig) error {
	pid, err := readDaemonPID(daemon.PIDFile)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("daemon is not running")
		return nil
	}
	if err != nil {
		return err
	}
	if !daemonRunning(pid) {
		_ = os.Remove(daemon.PIDFile)
		fmt.Println("daemon is not running (stale PID file removed)")
		return nil
	}
	if err := signalPID(pid, false); err != nil {
		return fmt.Errorf("stop daemon: %w", err)
	}
	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for daemonRunning(pid) {
		select {
		case <-deadline.C:
			if err := signalPID(pid, true); err != nil && !errors.Is(err, os.ErrProcessDone) {
				return fmt.Errorf("force stop daemon: %w", err)
			}
		case <-ticker.C:
		}
		if !daemonRunning(pid) {
			_ = os.Remove(daemon.PIDFile)
			fmt.Printf("daemon stopped (PID %d)\n", pid)
			return nil
		}
	}
	_ = os.Remove(daemon.PIDFile)
	fmt.Printf("daemon stopped (PID %d)\n", pid)
	return nil
}

func statusDaemon(daemon daemonConfig) error {
	pid, err := readDaemonPID(daemon.PIDFile)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("daemon is not running")
		return nil
	}
	if err != nil {
		return err
	}
	if daemonRunning(pid) {
		fmt.Printf("daemon is running (PID %d)\n", pid)
		return nil
	}
	fmt.Printf("daemon is not running (stale PID file, PID %d)\n", pid)
	return nil
}

func notifyMain(args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	service := fs.String("service", "", "configured service name")
	pid := fs.Int("pid", 0, "OOM JVM process ID")
	socket := fs.String("socket", filepath.Join(home, defaultSocketName), "Unix socket path")
	timeout := fs.Duration("timeout", 3*time.Second, "notification timeout")
	fs.Usage = func() { notifyUsage(fs.Output(), *socket) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *service == "" || *pid <= 1 {
		return errors.New("--service and a PID greater than 1 are required")
	}

	dialer := net.Dialer{Timeout: *timeout}
	conn, err := dialer.Dial("unix", expandHome(*socket, home))
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(*timeout))
	if err := json.NewEncoder(conn).Encode(request{Service: *service, PID: *pid}); err != nil {
		return fmt.Errorf("send notification: %w", err)
	}
	var reply response
	if err := json.NewDecoder(io.LimitReader(conn, maxMessageSize)).Decode(&reply); err != nil {
		return fmt.Errorf("read daemon response: %w", err)
	}
	if !reply.OK {
		return errors.New(reply.Error)
	}
	return nil
}

func loadConfig(path, home string) (config, error) {
	path = expandHome(path, home)
	info, err := os.Stat(path)
	if err != nil {
		return config{}, fmt.Errorf("stat config: %w", err)
	}
	if info.Mode().Perm()&0022 != 0 {
		return config{}, fmt.Errorf("config %s must not be writable by group or others", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg config
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Socket == "" {
		cfg.Socket = filepath.Join(home, defaultSocketName)
	} else {
		cfg.Socket = expandHome(cfg.Socket, home)
	}
	if len(cfg.Services) == 0 {
		return config{}, errors.New("config has no services")
	}
	for name, svc := range cfg.Services {
		svc.PIDFile = expandHome(svc.PIDFile, home)
		svc.WorkDir = expandHome(svc.WorkDir, home)
		svc.RestartLog = expandHome(svc.RestartLog, home)
		if svc.PIDFile == "" || svc.ProcessMatch == "" || len(svc.StartCommand) == 0 {
			return config{}, fmt.Errorf("service %q requires pid_file, process_match, and start_command", name)
		}
		if svc.ExitWait.Duration == 0 {
			svc.ExitWait.Duration = 30 * time.Second
		}
		if svc.StopTimeout.Duration == 0 {
			svc.StopTimeout.Duration = 10 * time.Second
		}
		if svc.StartTimeout.Duration == 0 {
			svc.StartTimeout.Duration = 30 * time.Second
		}
		if svc.VerifyTimeout.Duration == 0 {
			svc.VerifyTimeout.Duration = 30 * time.Second
		}
		if svc.RestartLogMax == 0 {
			svc.RestartLogMax = 10 * 1024 * 1024
		}
		if svc.RestartLimit == 0 {
			svc.RestartLimit = 5
		}
		if svc.RestartWindow.Duration == 0 {
			svc.RestartWindow.Duration = 10 * time.Minute
		}
		if svc.BackoffInitial.Duration == 0 {
			svc.BackoffInitial.Duration = 5 * time.Second
		}
		if svc.BackoffMax.Duration == 0 {
			svc.BackoffMax.Duration = 5 * time.Minute
		}
		if svc.RestartLimit < 1 || svc.RestartLimit > 1000 || svc.RestartWindow.Duration < 0 || svc.BackoffInitial.Duration < 0 || svc.BackoffMax.Duration < 0 {
			return config{}, fmt.Errorf("service %q has invalid restart protection settings", name)
		}
		if svc.RestartLogMax < 0 || svc.RestartLogFiles < 0 || svc.RestartLogDays < 0 {
			return config{}, fmt.Errorf("service %q has invalid restart log settings", name)
		}
		cfg.Services[name] = svc
	}
	return cfg, nil
}

func prepareSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to remove non-socket path %s", path)
	}
	conn, dialErr := net.DialTimeout("unix", path, 300*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		return fmt.Errorf("another daemon is already listening on %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

type handler struct {
	cfg       config
	mu        sync.Mutex
	active    map[string]bool
	history   map[string][]time.Time
	connSlots chan struct{}
	wg        sync.WaitGroup
}

func (h *handler) serveConn(ctx context.Context, conn net.Conn) {
	defer h.wg.Done()
	defer func() { <-h.connSlots }()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(io.LimitReader(conn, maxMessageSize+1))
	line, err := reader.ReadBytes('\n')
	if err != nil || len(line) > maxMessageSize {
		writeResponse(conn, response{Error: "invalid request"})
		return
	}
	var req request
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeResponse(conn, response{Error: "invalid JSON request"})
		return
	}
	svc, ok := h.cfg.Services[req.Service]
	if !ok || req.PID <= 1 {
		writeResponse(conn, response{Error: "unknown service or invalid PID"})
		return
	}
	if err := validatePID(req.PID, svc); err != nil {
		log.Printf("rejected service=%q pid=%d: %v", req.Service, req.PID, err)
		writeResponse(conn, response{Error: err.Error()})
		return
	}

	h.mu.Lock()
	if h.active[req.Service] {
		h.mu.Unlock()
		writeResponse(conn, response{Error: "restart already in progress"})
		return
	}
	now := time.Now()
	history := h.history[req.Service]
	cutoff := now.Add(-svc.RestartWindow.Duration)
	kept := history[:0]
	for _, at := range history {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= svc.RestartLimit {
		h.history[req.Service] = kept
		h.mu.Unlock()
		writeResponse(conn, response{Error: "restart circuit breaker is open"})
		log.Printf("restart circuit open service=%q attempts=%d window=%s", req.Service, len(kept), svc.RestartWindow.Duration)
		return
	}
	attempt := len(kept) + 1
	h.history[req.Service] = append(kept, now)
	h.active[req.Service] = true
	h.mu.Unlock()
	writeResponse(conn, response{OK: true})
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() {
			h.mu.Lock()
			delete(h.active, req.Service)
			h.mu.Unlock()
		}()
		if err := restart(ctx, req.Service, req.PID, svc, attempt); err != nil {
			log.Printf("restart failed service=%q pid=%d: %v", req.Service, req.PID, err)
		}
	}()
}

func writeResponse(w io.Writer, reply response) { _ = json.NewEncoder(w).Encode(reply) }

func validatePID(pid int, svc serviceConfig) error {
	data, err := os.ReadFile(svc.PIDFile)
	if err != nil {
		return fmt.Errorf("read PID file: %w", err)
	}
	want, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || want != pid {
		return fmt.Errorf("not the PID recorded in %s", svc.PIDFile)
	}
	command, err := processCommand(pid)
	if err != nil {
		return fmt.Errorf("inspect process: %w", err)
	}
	if !strings.Contains(command, svc.ProcessMatch) {
		return fmt.Errorf("process command does not contain configured process_match")
	}
	return nil
}

func processCommand(pid int) (string, error) {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		return strings.ReplaceAll(string(data), "\x00", " "), nil
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func restart(ctx context.Context, name string, pid int, svc serviceConfig, attempt int) error {
	identity, err := processIdentity(pid)
	if err != nil {
		return fmt.Errorf("capture process identity: %w", err)
	}
	processFD, _ := openProcessHandle(pid)
	defer closeProcessHandle(processFD)
	log.Printf("OOM accepted service=%q pid=%d; waiting up to %s for JVM exit", name, pid, svc.ExitWait.Duration)
	if !waitForExit(ctx, pid, identity, svc.ExitWait.Duration) {
		if !sameProcess(pid, identity) {
			return startAfterDelay(ctx, name, pid, svc, attempt)
		}
		log.Printf("pid=%d still alive; sending SIGTERM", pid)
		if err := signalProcessHandle(processFD, pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("SIGTERM: %w", err)
		}
		if !waitForExit(ctx, pid, identity, svc.StopTimeout.Duration) {
			if !sameProcess(pid, identity) {
				return startAfterDelay(ctx, name, pid, svc, attempt)
			}
			log.Printf("pid=%d still alive; sending SIGKILL", pid)
			if err := signalProcessHandle(processFD, pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("SIGKILL: %w", err)
			}
			if !waitForExit(ctx, pid, identity, 5*time.Second) {
				return errors.New("process did not disappear after SIGKILL")
			}
		}
	}
	return startAfterDelay(ctx, name, pid, svc, attempt)
}

func startAfterDelay(ctx context.Context, name string, oldPID int, svc serviceConfig, attempt int) error {
	delay := svc.RestartDelay.Duration + restartBackoff(svc, attempt)
	if delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return startService(ctx, name, oldPID, svc)
}

func restartBackoff(svc serviceConfig, attempt int) time.Duration {
	if attempt <= 0 || svc.BackoffInitial.Duration <= 0 {
		return 0
	}
	delay := svc.BackoffInitial.Duration
	for i := 1; i < attempt; i++ {
		if delay >= svc.BackoffMax.Duration/2 {
			return svc.BackoffMax.Duration
		}
		delay *= 2
	}
	if delay > svc.BackoffMax.Duration {
		return svc.BackoffMax.Duration
	}
	return delay
}

func waitForExit(ctx context.Context, pid int, identity string, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !sameProcess(pid, identity) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func processIdentity(pid int) (string, error) {
	// Linux /proc stat field 22 is the process start time. Combining it with the
	// PID prevents accidentally signalling an unrelated process after PID reuse.
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		line := string(data)
		end := strings.LastIndex(line, ")")
		if end >= 0 {
			fields := strings.Fields(line[end+1:])
			if len(fields) > 19 {
				return "proc:" + fields[19], nil
			}
		}
	}
	command, err := processCommand(pid)
	if err != nil {
		return "", err
	}
	return "command:" + command, nil
}

func sameProcess(pid int, identity string) bool {
	current, err := processIdentity(pid)
	return err == nil && current == identity
}

func startService(parent context.Context, name string, oldPID int, svc serviceConfig) error {
	ctx, cancel := context.WithTimeout(parent, svc.StartTimeout.Duration)
	defer cancel()
	cmd := exec.CommandContext(ctx, svc.StartCommand[0], svc.StartCommand[1:]...)
	cmd.Dir = svc.WorkDir
	cmd.Env = os.Environ()
	for key, value := range svc.Environment {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var logFile *os.File
	if svc.RestartLog != "" {
		var err error
		logger, loggerErr := newServiceLogWriter(svc)
		if loggerErr != nil {
			return fmt.Errorf("open restart log: %w", loggerErr)
		}
		logFile, err = logger.OpenForCommand()
		if err != nil {
			return fmt.Errorf("open restart log: %w", err)
		}
		defer logFile.Close()
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	log.Printf("starting service=%q command=%q", name, svc.StartCommand)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}
	deadline := time.NewTimer(svc.VerifyTimeout.Duration)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(svc.PIDFile)
		if err == nil {
			newPID, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && newPID > 1 && newPID != oldPID && validatePID(newPID, svc) == nil {
				log.Printf("service=%q started successfully pid=%d", name, newPID)
				return nil
			}
		}
		select {
		case <-parent.Done():
			return parent.Err()
		case <-deadline.C:
			return fmt.Errorf("new process was not verified within %s", svc.VerifyTimeout.Duration)
		case <-ticker.C:
		}
	}
}

func newServiceLogWriter(svc serviceConfig) (*rotatingLogWriter, error) {
	directory := filepath.Dir(svc.RestartLog)
	pattern := filepath.Base(svc.RestartLog)
	if svc.RestartLogFiles == 0 {
		svc.RestartLogFiles = 7
	}
	if svc.RestartLogDays == 0 {
		svc.RestartLogDays = 30
	}
	return newRollingLogWriter(directory, pattern, "text", true, svc.RestartLogMax, svc.RestartLogFiles, svc.RestartLogDays)
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
