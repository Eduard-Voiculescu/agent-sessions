import Foundation

/// Feed owns the CLI process it reads from, so the feed's lifetime is the pet's:
/// there is no socket to orphan and no daemon to install, and a feed that dies is
/// visible as a mood rather than as silence.
public final class Feed {
    private let binary: String
    private let onFrame: (Frame) -> Void
    private let onDisconnect: () -> Void

    private var process: Process?
    private var backoff: TimeInterval = 1
    private var buffer = Data()
    private let queue = DispatchQueue(label: "agent-pet.feed")

    public init(binary: String = "agent-sessions",
                onFrame: @escaping (Frame) -> Void,
                onDisconnect: @escaping () -> Void) {
        self.binary = binary
        self.onFrame = onFrame
        self.onDisconnect = onDisconnect
    }

    public func start() {
        queue.async { [weak self] in self?.spawn() }
    }

    public func stop() {
        queue.async { [weak self] in
            self?.process?.terminate()
            self?.process = nil
        }
    }

    private func spawn() {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = [binary, "watch"]

        let out = Pipe()
        process.standardOutput = out
        process.standardError = Pipe()

        out.fileHandleForReading.readabilityHandler = { [weak self] handle in
            self?.consume(handle.availableData)
        }
        process.terminationHandler = { [weak self] _ in
            self?.onDisconnect()
            self?.retry()
        }

        do {
            try process.run()
        } catch {
            onDisconnect()
            retry()
            return
        }
        self.process = process
        backoff = 1
    }

    /// consume reassembles lines across reads: a frame is one line, and a pipe
    /// read boundary lands wherever it likes.
    private func consume(_ data: Data) {
        guard !data.isEmpty else { return }
        buffer.append(data)

        while let newline = buffer.firstIndex(of: UInt8(ascii: "\n")) {
            let line = buffer[buffer.startIndex..<newline]
            buffer.removeSubrange(buffer.startIndex...newline)
            if let text = String(data: line, encoding: .utf8), let frame = FeedDecoder.decode(line: text) {
                onFrame(frame)
            }
        }
    }

    private func retry() {
        let delay = backoff
        backoff = min(backoff * 2, 30)
        queue.asyncAfter(deadline: .now() + delay) { [weak self] in self?.spawn() }
    }
}
