import CoreGraphics
import Darwin
import Foundation

/// Measures executable-start to first on-screen app window without using
/// LaunchServices. Run a warm-up first; report distribution, never gate CI on
/// a single wall-clock threshold.
@main
struct AppLaunchBenchmark {
    static func main() {
        guard CommandLine.arguments.count == 2 else {
            fputs("usage: app-launch-benchmark /Applications/Product.app/Contents/MacOS/Executable\n", stderr)
            exit(2)
        }
        let executable = CommandLine.arguments[1]
        var samples: [Double] = []
        for iteration in 0..<11 {
            guard let elapsed = launchToFirstWindow(executable) else {
                fputs("app did not create a visible window\n", stderr)
                exit(1)
            }
            if iteration > 0 { samples.append(elapsed) }
        }
        samples.sort()
        let median = samples[samples.count / 2]
        let p95 = samples[Int(Double(samples.count - 1) * 0.95)]
        print(String(format: "first window p50 %.1f ms · p95 %.1f ms · n=%d", median, p95, samples.count))
    }

    private static func launchToFirstWindow(_ executable: String) -> Double? {
        var pid: pid_t = 0
        let argument = strdup(executable)
        defer { free(argument) }
        var argv: [UnsafeMutablePointer<CChar>?] = [argument, nil]
        let started = CFAbsoluteTimeGetCurrent()
        let code = executable.withCString { path in
            posix_spawn(&pid, path, nil, nil, &argv, environ)
        }
        guard code == 0 else { return nil }

        let deadline = started + 5
        var firstWindow: Double?
        while CFAbsoluteTimeGetCurrent() < deadline {
            if hasVisibleWindow(pid: pid) {
                firstWindow = (CFAbsoluteTimeGetCurrent() - started) * 1_000
                break
            }
            usleep(1_000)
        }

        Darwin.kill(pid, SIGTERM)
        var status: Int32 = 0
        while Darwin.waitpid(pid, &status, 0) == -1 && errno == EINTR {}
        return firstWindow
    }

    private static func hasVisibleWindow(pid: pid_t) -> Bool {
        guard let windows = CGWindowListCopyWindowInfo(
            [.optionOnScreenOnly, .excludeDesktopElements],
            kCGNullWindowID
        ) as? [[String: Any]] else { return false }
        return windows.contains { window in
            guard (window[kCGWindowOwnerPID as String] as? Int32) == pid,
                  (window[kCGWindowLayer as String] as? Int) == 0,
                  let bounds = window[kCGWindowBounds as String] as? [String: CGFloat]
            else { return false }
            return (bounds["Width"] ?? 0) > 100 && (bounds["Height"] ?? 0) > 100
        }
    }
}
