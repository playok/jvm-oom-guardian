import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

/** Integration-test-only JVM that either exhausts its heap or stays healthy. */
public final class OomTestDaemon {
    private static void write(Path path, String value) throws IOException {
        Files.writeString(path, value, StandardOpenOption.CREATE,
                StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE);
    }

    public static void main(String[] args) throws Exception {
        if (args.length != 3) {
            throw new IllegalArgumentException("usage: OomTestDaemon <oom|healthy> <pid-file> <ready-file>");
        }
        String mode = args[0];
        Path pidFile = Path.of(args[1]);
        Path readyFile = Path.of(args[2]);
        long pid = ProcessHandle.current().pid();
        write(pidFile, Long.toString(pid));
        System.out.printf("%s pid=%d mode=%s%n", Instant.now(), pid, mode);

        if (mode.equals("healthy")) {
            write(readyFile, "pid=" + pid + " ready=" + Instant.now() + System.lineSeparator());
            Thread.sleep(120_000);
            return;
        }
        if (!mode.equals("oom")) {
            throw new IllegalArgumentException("unknown mode: " + mode);
        }

        Thread.sleep(1_000);
        List<byte[]> retained = new ArrayList<>();
        while (true) {
            retained.add(new byte[1024 * 1024]);
        }
    }
}
