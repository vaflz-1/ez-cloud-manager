import Darwin
import Foundation

struct FastProcessResult {
    let terminationStatus: Int32
    let terminationSignal: Int32?
    let standardOutput: Data
    let standardError: Data
}

/// Thread-safe cancellation handle for a spawned core command. Every core
/// invocation runs in its own process group, so cancelling an interactive
/// cloud login terminates both the bundled core and the vendor CLI/browser
/// helper it started. The delayed SIGKILL is guarded by the exact live pid;
/// it can never target a later process that reused the numeric pid.
final class FastProcessCancellation: @unchecked Sendable {
    private let lock = NSLock()
    private var processGroup: pid_t?
    private var cancelled = false

    var isCancelled: Bool {
        lock.lock()
        defer { lock.unlock() }
        return cancelled
    }

    func cancel() {
        lock.lock()
        cancelled = true
        let group = processGroup
        if let group { signal(group: group, signal: SIGTERM) }
        lock.unlock()
        guard let group else { return }
        scheduleForcedTermination(of: group)
    }

    fileprivate func register(processGroup group: pid_t) {
        lock.lock()
        processGroup = group
        let shouldCancel = cancelled
        if shouldCancel { signal(group: group, signal: SIGTERM) }
        lock.unlock()
        if shouldCancel {
            scheduleForcedTermination(of: group)
        }
    }

    fileprivate func finish(processGroup group: pid_t) {
        lock.lock()
        if processGroup == group { processGroup = nil }
        lock.unlock()
    }

    private func scheduleForcedTermination(of group: pid_t) {
        DispatchQueue.global(qos: .userInitiated).asyncAfter(deadline: .now() + 2) { [weak self] in
            guard let self else { return }
            self.lock.lock()
            let stillRunning = self.cancelled && self.processGroup == group
            if stillRunning { self.signal(group: group, signal: SIGKILL) }
            self.lock.unlock()
        }
    }

    private func signal(group: pid_t, signal: Int32) {
        // Negative pid addresses the process group created by posix_spawn.
        if Darwin.kill(-group, signal) == -1, errno != ESRCH {
            // Cancellation is best-effort at this boundary. waitpid remains
            // authoritative and will surface the actual termination result.
        }
    }
}

/// A small `posix_spawn` boundary for the bundled core. `Foundation.Process`
/// adds tens of milliseconds to every otherwise 2–3 ms local command on
/// macOS. This runner keeps the same process isolation, exact argv and
/// request-scoped environment while avoiding that framework overhead.
enum FastProcessRunner {
    private final class Drain: @unchecked Sendable {
        private let handle: FileHandle
        private let lock = NSLock()
        private var stored = Data()

        init(fileDescriptor: Int32) {
            handle = FileHandle(fileDescriptor: fileDescriptor, closeOnDealloc: true)
        }

        func start(in group: DispatchGroup) {
            group.enter()
            DispatchQueue.global(qos: .userInitiated).async { [self] in
                defer { group.leave() }
                let value = handle.readDataToEndOfFile()
                lock.lock()
                stored = value
                lock.unlock()
            }
        }

        func data() -> Data {
            lock.lock()
            defer { lock.unlock() }
            return stored
        }
    }

    private final class InputWriter: @unchecked Sendable {
        private let handle: FileHandle
        private let input: Data?
        private let lock = NSLock()
        private var storedError: Error?

        init(fileDescriptor: Int32, input: Data?) {
            handle = FileHandle(fileDescriptor: fileDescriptor, closeOnDealloc: true)
            self.input = input
        }

        func start(in group: DispatchGroup) {
            group.enter()
            DispatchQueue.global(qos: .userInitiated).async { [self] in
                defer { group.leave() }
                if let input {
                    do {
                        try handle.write(contentsOf: input)
                    } catch {
                        lock.lock()
                        storedError = error
                        lock.unlock()
                    }
                }
                try? handle.close()
            }
        }

        func error() -> Error? {
            lock.lock()
            defer { lock.unlock() }
            return storedError
        }
    }

    private struct PipePair {
        let read: Int32
        let write: Int32

        func closeBoth() {
            if read >= 0 { Darwin.close(read) }
            if write >= 0 { Darwin.close(write) }
        }
    }

    static func run(
        executable: String,
        arguments: [String],
        input: Data?,
        environment: [String: String],
        cancellation: FastProcessCancellation? = nil
    ) throws -> FastProcessResult {
        var pipes: [PipePair] = []
        var childWasSpawned = false
        do {
            pipes.append(try makePipe(protectWriterFromSIGPIPE: true))
            pipes.append(try makePipe())
            pipes.append(try makePipe())
            return try spawn(
                executable: executable,
                arguments: arguments,
                input: input,
                environment: environment,
                stdinPipe: pipes[0],
                stdoutPipe: pipes[1],
                stderrPipe: pipes[2],
                cancellation: cancellation,
                childWasSpawned: &childWasSpawned
            )
        } catch {
            // Once the child exists, `spawn` has either closed a descriptor
            // or transferred its sole ownership to a FileHandle. Never close
            // the raw numbers again: another concurrent spawn may already
            // have reused them.
            if !childWasSpawned {
                for pipe in pipes { pipe.closeBoth() }
            }
            throw error
        }
    }

    private static func spawn(
        executable: String,
        arguments: [String],
        input: Data?,
        environment: [String: String],
        stdinPipe: PipePair,
        stdoutPipe: PipePair,
        stderrPipe: PipePair,
        cancellation: FastProcessCancellation?,
        childWasSpawned: inout Bool
    ) throws -> FastProcessResult {
        var actions: posix_spawn_file_actions_t? = nil
        var attributes: posix_spawnattr_t? = nil
        try check(posix_spawn_file_actions_init(&actions), operation: "initialize spawn file actions")
        defer { posix_spawn_file_actions_destroy(&actions) }
        try check(posix_spawnattr_init(&attributes), operation: "initialize spawn attributes")
        defer { posix_spawnattr_destroy(&attributes) }

        // Close every unrelated descriptor in the child. The three explicit
        // dup2 actions below are the complete IPC surface.
        try check(posix_spawnattr_setpgroup(&attributes, 0), operation: "create process group")
        try check(
            posix_spawnattr_setflags(
                &attributes,
                Int16(POSIX_SPAWN_CLOEXEC_DEFAULT | POSIX_SPAWN_SETPGROUP)
            ),
            operation: "configure spawn descriptor policy"
        )
        try check(
            posix_spawn_file_actions_adddup2(&actions, stdinPipe.read, STDIN_FILENO),
            operation: "connect child stdin"
        )
        try check(
            posix_spawn_file_actions_adddup2(&actions, stdoutPipe.write, STDOUT_FILENO),
            operation: "connect child stdout"
        )
        try check(
            posix_spawn_file_actions_adddup2(&actions, stderrPipe.write, STDERR_FILENO),
            operation: "connect child stderr"
        )
        for descriptor in [
            stdinPipe.read, stdinPipe.write,
            stdoutPipe.read, stdoutPipe.write,
            stderrPipe.read, stderrPipe.write
        ] where descriptor > STDERR_FILENO {
            try check(
                posix_spawn_file_actions_addclose(&actions, descriptor),
                operation: "close child pipe descriptor"
            )
        }

        var argv = try duplicateCStringArray([executable] + arguments, kind: "argument")
        defer { freeCStringArray(&argv) }
        let environmentStrings = try environment.keys.sorted().map { key -> String in
            guard !key.isEmpty, !key.contains("=") else {
                throw posixError(EINVAL, operation: "validate environment key")
            }
            return "\(key)=\(environment[key] ?? "")"
        }
        var envp = try duplicateCStringArray(environmentStrings, kind: "environment value")
        defer { freeCStringArray(&envp) }

        var pid: pid_t = 0
        let spawnCode = executable.withCString { path in
            posix_spawn(&pid, path, &actions, &attributes, &argv, &envp)
        }
        guard spawnCode == 0 else {
            throw posixError(spawnCode, operation: "launch bundled core")
        }
        childWasSpawned = true
        cancellation?.register(processGroup: pid)
        defer { cancellation?.finish(processGroup: pid) }

        // Ownership of the opposite pipe ends now belongs to the child.
        Darwin.close(stdinPipe.read)
        Darwin.close(stdoutPipe.write)
        Darwin.close(stderrPipe.write)

        let io = DispatchGroup()
        let outputDrain = Drain(fileDescriptor: stdoutPipe.read)
        let errorDrain = Drain(fileDescriptor: stderrPipe.read)
        let inputWriter = InputWriter(fileDescriptor: stdinPipe.write, input: input)
        outputDrain.start(in: io)
        errorDrain.start(in: io)
        inputWriter.start(in: io)

        var rawStatus: Int32 = 0
        var waitResult: pid_t
        repeat {
            waitResult = Darwin.waitpid(pid, &rawStatus, 0)
        } while waitResult == -1 && errno == EINTR
        let waitError = waitResult == -1 ? errno : 0

        io.wait()
        if waitError != 0 {
            throw posixError(waitError, operation: "wait for bundled core")
        }
        if let inputError = inputWriter.error(), exitStatus(rawStatus) == 0 {
            throw inputError
        }

        return FastProcessResult(
            terminationStatus: exitStatus(rawStatus),
            terminationSignal: terminationSignal(rawStatus),
            standardOutput: outputDrain.data(),
            standardError: errorDrain.data()
        )
    }

    private static func makePipe(protectWriterFromSIGPIPE: Bool = false) throws -> PipePair {
        var descriptors: [Int32] = [-1, -1]
        guard Darwin.pipe(&descriptors) == 0 else {
            throw posixError(errno, operation: "create process pipe")
        }
        for descriptor in descriptors {
            if Darwin.fcntl(descriptor, F_SETFD, FD_CLOEXEC) == -1 {
                let code = errno
                Darwin.close(descriptors[0])
                Darwin.close(descriptors[1])
                throw posixError(code, operation: "protect process pipe")
            }
        }
        if protectWriterFromSIGPIPE,
           Darwin.fcntl(descriptors[1], F_SETNOSIGPIPE, 1) == -1 {
            let code = errno
            Darwin.close(descriptors[0])
            Darwin.close(descriptors[1])
            throw posixError(code, operation: "protect process input pipe")
        }
        return PipePair(read: descriptors[0], write: descriptors[1])
    }

    private static func duplicateCStringArray(
        _ strings: [String],
        kind: String
    ) throws -> [UnsafeMutablePointer<CChar>?] {
        var pointers: [UnsafeMutablePointer<CChar>?] = []
        pointers.reserveCapacity(strings.count + 1)
        do {
            for value in strings {
                guard !value.utf8.contains(0) else {
                    throw posixError(EINVAL, operation: "validate process \(kind)")
                }
                guard let copy = strdup(value) else {
                    throw posixError(ENOMEM, operation: "allocate process \(kind)")
                }
                pointers.append(copy)
            }
            pointers.append(nil)
            return pointers
        } catch {
            freeCStringArray(&pointers)
            throw error
        }
    }

    private static func freeCStringArray(_ pointers: inout [UnsafeMutablePointer<CChar>?]) {
        for pointer in pointers {
            if let pointer { free(pointer) }
        }
        pointers.removeAll(keepingCapacity: false)
    }

    private static func exitStatus(_ rawStatus: Int32) -> Int32 {
        let signal = rawStatus & 0x7f
        return signal == 0 ? (rawStatus >> 8) & 0xff : 128 + signal
    }

    private static func terminationSignal(_ rawStatus: Int32) -> Int32? {
        let signal = rawStatus & 0x7f
        return signal == 0 ? nil : signal
    }

    private static func check(_ code: Int32, operation: String) throws {
        guard code == 0 else { throw posixError(code, operation: operation) }
    }

    private static func posixError(_ code: Int32, operation: String) -> NSError {
        NSError(
            domain: NSPOSIXErrorDomain,
            code: Int(code),
            userInfo: [NSLocalizedDescriptionKey: "\(operation): \(String(cString: strerror(code)))"]
        )
    }
}
