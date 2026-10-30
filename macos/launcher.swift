import AppKit
import Foundation

let env = ProcessInfo.processInfo.environment
let fm = FileManager.default

func fail(_ message: String) -> Never {
    if env["NALMEOK_DRY_RUN"] == "1" {
        FileHandle.standardError.write((message + "\n").data(using: .utf8)!)
        exit(1)
    }
    let alert = NSAlert()
    alert.messageText = "nalmeok을 실행할 수 없습니다"
    alert.informativeText = message
    alert.alertStyle = .critical
    alert.runModal()
    exit(1)
}

guard let resources = Bundle.main.resourceURL else {
    fail("앱 리소스를 찾지 못했습니다.")
}

let dataRoot: URL
if let override = env["NALMEOK_HOME"], !override.isEmpty {
    dataRoot = URL(fileURLWithPath: override, isDirectory: true)
} else {
    guard let support = fm.urls(for: .applicationSupportDirectory,
                                in: .userDomainMask).first else {
        fail("Application Support 폴더를 찾지 못했습니다.")
    }
    dataRoot = support.appendingPathComponent("nalmeok", isDirectory: true)
}

let appDir = dataRoot.appendingPathComponent("app", isDirectory: true)
do {
    try fm.createDirectory(at: appDir, withIntermediateDirectories: true)
    let target = appDir.appendingPathComponent("nalmeok/__init__.py")
    if !fm.fileExists(atPath: target.path) {
        let seed = resources.appendingPathComponent("nalmeok", isDirectory: true)
        let dest = appDir.appendingPathComponent("nalmeok", isDirectory: true)
        try? fm.removeItem(at: dest)
        try fm.copyItem(at: seed, to: dest)
    }
} catch {
    fail("앱 데이터를 준비하지 못했습니다: \(error.localizedDescription)")
}

let bootstrap = resources.appendingPathComponent("bootstrap.command")
guard fm.isExecutableFile(atPath: bootstrap.path) else {
    fail("bootstrap.command를 찾지 못했습니다. 앱을 다시 설치해 주세요.")
}

if env["NALMEOK_DRY_RUN"] == "1" {
    print(bootstrap.path)
    exit(0)
}

if !NSWorkspace.shared.open(bootstrap) {
    fail("Terminal을 열지 못했습니다.")
}
