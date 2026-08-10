import Darwin
import Foundation

@main
struct FastProcessRunnerSmoke {
    static func require(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() { fputs("FAIL: \(message)\n", stderr); exit(1) }
    }

    static func run(
        _ executable: String,
        _ arguments: [String],
        input: Data? = nil,
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) throws -> FastProcessResult {
        try FastProcessRunner.run(
            executable: executable,
            arguments: arguments,
            input: input,
            environment: environment
        )
    }

    static func openFDCount() -> Int {
        var count = 0
        for fd in Int32(0)..<Int32(4096) {
            errno = 0
            if fcntl(fd, F_GETFD) != -1 || errno != EBADF { count += 1 }
        }
        return count
    }

    static func main() throws {
        let unicode = "argument with spaces · облако\n"
        let echoed = try run("/bin/cat", [], input: Data(unicode.utf8))
        require(echoed.terminationStatus == 0, "cat status")
        require(String(data: echoed.standardOutput, encoding: .utf8) == unicode, "unicode roundtrip")
        let argument = "argument with spaces · облако"
        let argumentEcho = try run("/bin/echo", [argument])
        require(String(data: argumentEcho.standardOutput, encoding: .utf8) == argument + "\n", "unicode argv")

        var env = ProcessInfo.processInfo.environment
        env["FAST_PROCESS_SENTINEL"] = "workspace value"
        let environment = try run(
            "/bin/sh",
            ["-c", "printf %s \"$FAST_PROCESS_SENTINEL\""],
            environment: env
        )
        require(String(data: environment.standardOutput, encoding: .utf8) == "workspace value", "environment")

        let failed = try run("/bin/sh", ["-c", "printf failure >&2; exit 23"])
        require(failed.terminationStatus == 23, "nonzero status")
        require(String(data: failed.standardError, encoding: .utf8) == "failure", "stderr")

        let largeInput = Data(repeating: 0x5a, count: 2 * 1024 * 1024)
        let largeEcho = try run("/bin/cat", [], input: largeInput)
        require(largeEcho.standardOutput == largeInput, "2 MiB stdin")

        let dual = try run(
            "/bin/sh",
            ["-c", "/bin/dd if=/dev/zero bs=1048576 count=4 2>/dev/null; /bin/dd if=/dev/zero bs=1048576 count=4 1>&2 2>/dev/null"]
        )
        require(dual.standardOutput.count == 4 * 1024 * 1024, "4 MiB stdout")
        require(dual.standardError.count == 4 * 1024 * 1024, "4 MiB stderr")

        do {
            _ = try run("/usr/bin/true", [], input: Data(repeating: 1, count: 2 * 1024 * 1024))
        } catch let error as NSError {
            require(error.domain == NSCocoaErrorDomain || error.domain == NSPOSIXErrorDomain, "early stdin close")
        }

        let signaled = try run("/bin/sh", ["-c", "kill -TERM $$"])
        require(signaled.terminationSignal == SIGTERM, "signal reported")
        require(signaled.terminationStatus == 128 + SIGTERM, "signal status")

        do {
            _ = try run("/definitely/missing/fast-process", [])
            require(false, "missing executable")
        } catch let error as NSError {
            require(error.domain == NSPOSIXErrorDomain && error.code == Int(ENOENT), "ENOENT")
        }
        do {
            _ = try run("/usr/bin/true", [], environment: ["INVALID=KEY": "value"])
            require(false, "invalid environment key")
        } catch let error as NSError {
            require(error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL), "environment validation")
        }
        do {
            _ = try run("/usr/bin/true", ["invalid\0argument"])
            require(false, "NUL argument")
        } catch let error as NSError {
            require(error.domain == NSPOSIXErrorDomain && error.code == Int(EINVAL), "argument validation")
        }

        let before = openFDCount()
        for _ in 0..<300 {
            let repeated = try run("/usr/bin/true", [])
            require(repeated.terminationStatus == 0, "repeat")
        }
        let after = openFDCount()
        require(after <= before + 1, "fd leak: before \(before), after \(after)")

        let group = DispatchGroup()
        let lock = NSLock()
        var failures = 0
        for index in 0..<50 {
            group.enter()
            DispatchQueue.global().async {
                defer { group.leave() }
                do {
                    let value = "\(index)"
                    if String(data: try run("/bin/echo", [value]).standardOutput, encoding: .utf8) != value + "\n" {
                        lock.lock(); failures += 1; lock.unlock()
                    }
                } catch {
                    lock.lock(); failures += 1; lock.unlock()
                }
            }
        }
        group.wait()
        require(failures == 0, "50 concurrent calls")

        if CommandLine.arguments.count > 1 {
            let core = CommandLine.arguments[1]
            var durations: [Double] = []
            for _ in 0..<100 {
                let start = CFAbsoluteTimeGetCurrent()
                let result = try run(core, ["app", "bootstrap"])
                require(result.terminationStatus == 0 && !result.standardOutput.isEmpty, "bundled core parity")
                durations.append((CFAbsoluteTimeGetCurrent() - start) * 1_000)
            }
            durations.sort()
            let median = durations[durations.count / 2]
            let p95 = durations[Int(Double(durations.count - 1) * 0.95)]
            print(String(format: "FastProcessRunner bootstrap p50 %.2f ms · p95 %.2f ms", median, p95))
        }
        print("FastProcessRunner smoke: PASS")
    }
}
