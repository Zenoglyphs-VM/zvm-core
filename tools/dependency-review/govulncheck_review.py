#!/usr/bin/env python3
"""Bound govulncheck execution and retain an explicitly scoped review result."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import math
import os
from pathlib import Path
import re
import selectors
import signal
import stat
import subprocess
import sys
import time

MAX_STDOUT = 8 << 20
MAX_STDERR = 256 << 10
MAX_MESSAGES = 20000
PROTOCOL = "v1.0.0"
SCANNER_VERSION = "v1.9.0"
DATABASE = "https://vuln.go.dev"
GO_ID = re.compile(r"GO-\d{4}-\d{4,}")
PORTABLE = re.compile(r"[A-Za-z0-9._~!+/@-]{1,512}")


class ReviewError(ValueError):
    """An input cannot support the selected review contract."""


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ReviewError("duplicate JSON member")
        result[key] = value
    return result


def integer(raw):
    if len(raw) > 20:
        raise ReviewError("JSON integer exceeds its bound")
    return int(raw)


def reject_constant(_):
    raise ReviewError("non-finite JSON number")


def real(raw):
    value = float(raw)
    if not math.isfinite(value):
        raise ReviewError("non-finite JSON number")
    return value


def messages(raw):
    if not raw or len(raw) > MAX_STDOUT:
        raise ReviewError("empty or oversized stream")
    decoder = json.JSONDecoder(object_pairs_hook=unique_object, parse_int=integer, parse_float=real,
                               parse_constant=reject_constant)
    text = raw.decode("utf-8")
    offset, count = 0, 0
    while offset < len(text):
        while offset < len(text) and text[offset] in " \t\r\n":
            offset += 1
        if offset == len(text):
            break
        value, offset = decoder.raw_decode(text, offset)
        count += 1
        if count > MAX_MESSAGES or not isinstance(value, dict) or len(value) != 1:
            raise ReviewError("invalid bounded protocol message")
        yield value


def text_field(value, name, required=False, maximum=512):
    result = value.get(name, "")
    if not isinstance(result, str) or len(result) > maximum or (required and not result):
        raise ReviewError("invalid protocol string")
    return result


def timestamp(value):
    if not isinstance(value, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z", value):
        raise ReviewError("missing database observation time")
    datetime.fromisoformat(value.split(".")[0].removesuffix("Z") + "+00:00")
    return value


def portable(value):
    return isinstance(value, str) and PORTABLE.fullmatch(value) and not value.startswith(("/", "~")) and not any(
        part in (".", "..") for part in value.split("/"))


def summarize(raw, *, go_version, mode="source", scanner_version=SCANNER_VERSION, database=DATABASE):
    """Classify scanner reports, without independently asserting reachability."""
    if mode not in ("source", "binary"):
        raise ReviewError("unsupported review mode")
    config = sbom = None
    advisories, findings = {}, []
    count = 0
    for message in messages(raw):
        count += 1
        kind, value = next(iter(message.items()))
        if not isinstance(value, dict) or (count == 1 and kind != "config"):
            raise ReviewError("missing initial scanner configuration")
        if kind == "config":
            allowed = {"protocol_version", "scanner_name", "scanner_version", "db", "db_last_modified",
                       "go_version", "scan_level", "scan_mode"}
            if config is not None or set(value) - allowed:
                raise ReviewError("ambiguous scanner configuration")
            expected = {"protocol_version": PROTOCOL, "scanner_name": "govulncheck",
                        "scanner_version": scanner_version, "db": database,
                        "scan_level": "symbol", "scan_mode": mode}
            if any(value.get(key) != wanted for key, wanted in expected.items()):
                raise ReviewError("scanner contract differs from independent expectations")
            if mode == "source" and value.get("go_version") != go_version:
                raise ReviewError("source analysis Go version differs")
            timestamp(value.get("db_last_modified"))
            config = value
        elif kind == "SBOM":
            if sbom is not None or set(value) - {"go_version", "modules", "roots"}:
                raise ReviewError("ambiguous scan inventory")
            if value.get("go_version") != go_version:
                raise ReviewError("scan inventory Go version differs")
            roots, modules = value.get("roots"), value.get("modules")
            if not isinstance(roots, list) or not roots or len(roots) > 4096:
                raise ReviewError("missing bounded scan roots")
            if not all(portable(root) for root in roots):
                raise ReviewError("invalid scan root")
            if not isinstance(modules, list) or not modules or len(modules) > 4096:
                raise ReviewError("missing bounded module inventory")
            selected = set()
            for module in modules:
                if not isinstance(module, dict) or set(module) - {"path", "version"}:
                    raise ReviewError("invalid inventory module")
                path = text_field(module, "path", required=True)
                version = text_field(module, "version", maximum=256)
                if not portable(path) or (path, version) in selected:
                    raise ReviewError("invalid or duplicate inventory module")
                selected.add((path, version))
            sbom = value
        elif kind == "osv":
            identifier = text_field(value, "id", required=True)
            if not GO_ID.fullmatch(identifier):
                raise ReviewError("invalid advisory")
            canonical = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()
            digest = sha(canonical)
            if identifier in advisories and advisories[identifier] != digest:
                raise ReviewError("conflicting advisory revisions")
            advisories[identifier] = digest
        elif kind == "finding":
            if set(value) - {"osv", "fixed_version", "trace"}:
                raise ReviewError("unknown finding fields")
            identifier = text_field(value, "osv", required=True)
            if not GO_ID.fullmatch(identifier):
                raise ReviewError("invalid finding identifier")
            fixed = text_field(value, "fixed_version", maximum=256)
            if fixed and not portable(fixed):
                raise ReviewError("invalid fixed version")
            trace = value.get("trace")
            if not isinstance(trace, list) or not trace or len(trace) > 256:
                raise ReviewError("missing bounded finding trace")
            for frame in trace:
                if not isinstance(frame, dict) or set(frame) - {"module", "version", "package", "function", "receiver", "position"}:
                    raise ReviewError("invalid finding frame")
                for key in ("module", "version", "package"):
                    item = text_field(frame, key, required=(key == "module"))
                    if item and not portable(item):
                        raise ReviewError("nonportable finding identifier")
                text_field(frame, "function", maximum=1024)
                text_field(frame, "receiver", maximum=1024)
                if "position" in frame:
                    pos = frame["position"]
                    if not isinstance(pos, dict) or set(pos) - {"filename", "offset", "line", "column"}:
                        raise ReviewError("invalid source position")
                    text_field(pos, "filename", maximum=4096)
                    if any(type(pos.get(k, 0)) is not int or pos.get(k, 0) < 0 for k in ("offset", "line", "column")):
                        raise ReviewError("invalid source coordinates")
            first = trace[0]
            package, function = first.get("package", ""), first.get("function", "")
            if function and not package:
                raise ReviewError("symbol without a package")
            if not function and (len(trace) != 1 or first.get("receiver") or first.get("position")):
                raise ReviewError("ambiguous lower-level finding")
            if mode == "binary" and (len(trace) != 1 or any("position" in frame for frame in trace)):
                raise ReviewError("binary result cannot establish a source call trace")
            level = ("source_call_trace_reported" if mode == "source" else "compiled_symbol_reported") if function else (
                "package_reported" if package else "module_reported")
            findings.append({"id": identifier, "level": level, "module": first["module"],
                             "version": first.get("version", ""), "package": package,
                             "fixedVersion": fixed or None})
        elif kind == "progress":
            if set(value) - {"time", "message"}:
                raise ReviewError("unknown progress fields")
        else:
            raise ReviewError("unsupported protocol message")
    if config is None or sbom is None or any(row["id"] not in advisories for row in findings):
        raise ReviewError("incomplete configuration, inventory or advisory references")
    levels = {name: 0 for name in ("module_reported", "package_reported", "source_call_trace_reported", "compiled_symbol_reported")}
    distinct = {}
    for row in findings:
        levels[row["level"]] += 1
        key = json.dumps(row, sort_keys=True)
        distinct[key] = row
    return {"schema": 1, "status": "REVIEW_REQUIRED" if findings else "NO_FINDINGS_REPORTED_IN_THIS_SCAN",
        "scannerVersion": scanner_version, "scanMode": mode, "scanLevel": "symbol", "analysisGoVersion": go_version,
        "database": database, "databaseModifiedObserved": config["db_last_modified"],
        "databaseSnapshotAuthenticated": False, "messages": count, "moduleRecords": len(sbom["modules"]),
        "roots": sorted(sbom["roots"]), "advisoryRecords": len(advisories), "advisoryDigests": dict(sorted(advisories.items())),
        "uniqueFindingAdvisories": len({row["id"] for row in findings}), "findingOccurrencesByLevel": levels,
        "findings": sorted(distinct.values(), key=lambda row: (row["id"], row["module"], row["package"], row["level"])),
        "runtimeExploitabilityEstablished": False, "unreportedMeansUnaffected": False,
        "replacementForkAdvisoryCoverageEstablished": False, "licenseReviewComplete": False, "releaseAccepted": False}


def run_bounded(argv, output, *, cwd=None, env=None, timeout=600, stdout_limit=MAX_STDOUT, stderr_limit=MAX_STDERR):
    """POSIX foreground capture with caps, a deadline and process-group teardown."""
    if os.name != "posix" or timeout <= 0 or stdout_limit <= 0 or stderr_limit <= 0:
        raise ReviewError("unsupported process budget")
    output = Path(output)
    names = {"stdout": "govulncheck.json", "stderr": "govulncheck.stderr"}
    files, streams = {}, {}
    proc = selector = None
    reason = None
    complete = {"stdout": False, "stderr": False}
    counts = {"stdout": 0, "stderr": 0}
    hashes = {name: hashlib.sha256() for name in names}
    caps = {"stdout": stdout_limit, "stderr": stderr_limit}
    started = time.monotonic()
    try:
        for name, path in names.items():
            files[name] = (output / path).open("xb")
        proc = subprocess.Popen(argv, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        selector = selectors.DefaultSelector()
        for name, stream in (("stdout", proc.stdout), ("stderr", proc.stderr)):
            os.set_blocking(stream.fileno(), False)
            selector.register(stream, selectors.EVENT_READ, name)
            streams[name] = stream
        deadline = started + timeout
        stopped_at = parent_exited_at = None
        while selector.get_map() or proc.poll() is None:
            now = time.monotonic()
            if proc.poll() is not None and parent_exited_at is None:
                parent_exited_at = now
            if reason is None and now >= deadline:
                reason = "deadline_exceeded"
            if reason is None and parent_exited_at is not None and now - parent_exited_at >= 2 and selector.get_map():
                reason = "incomplete_inherited_streams"
            if reason is not None and stopped_at is None:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                stopped_at = now
            if stopped_at is not None and now - stopped_at >= 3:
                break
            for key, _ in selector.select(0.05):
                name = key.data
                try:
                    block = os.read(key.fileobj.fileno(), 65536)
                except BlockingIOError:
                    continue
                if not block:
                    complete[name] = True
                    selector.unregister(key.fileobj)
                    continue
                remaining = max(0, caps[name] - counts[name])
                prefix = block[:remaining]
                if prefix:
                    files[name].write(prefix)
                    hashes[name].update(prefix)
                    counts[name] += len(prefix)
                if len(block) > remaining and reason is None:
                    reason = name + "_limit_exceeded"
        try:
            actual_exit = proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            actual_exit = None
            reason = reason or "process_termination_unknown"
        return {"actualExit": actual_exit, "terminationReason": reason,
            "outputComplete": reason is None and all(complete.values()),
            "seconds": round(time.monotonic() - started, 3),
            "streams": {name: {"path": names[name], "bytes": counts[name], "sha256": hashes[name].hexdigest(),
                                "eofObserved": complete[name], "limit": caps[name]} for name in names}}
    finally:
        if proc is not None and proc.poll() is None:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait(timeout=3)
        if selector is not None:
            selector.close()
        for stream in streams.values():
            stream.close()
        for stream in files.values():
            stream.flush()
            os.fsync(stream.fileno())
            stream.close()


def file_bytes(path, limit):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size > limit:
            raise ReviewError("selected file is not an independent bounded regular file")
        raw = source.read(limit + 1)
        after = os.fstat(source.fileno())
        identity = lambda item: (item.st_dev, item.st_ino, item.st_mode, item.st_nlink, item.st_size, item.st_mtime_ns, item.st_ctime_ns)
        if len(raw) != info.st_size or identity(info) != identity(after) or identity(after) != identity(path.lstat()):
            raise ReviewError("selected file changed during observation")
        return raw


def file_digest(path, limit):
    return sha(file_bytes(path, limit))


def source_binding(expected):
    if not re.fullmatch(r"[0-9a-f]{40}", expected):
        raise ReviewError("missing exact source revision")
    def git(*args):
        done = subprocess.run(["git", "--no-optional-locks", "--no-replace-objects", *args],
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
        if done.returncode or len(done.stdout) > 65536:
            raise ReviewError("source binding command failed")
        return done.stdout.decode().strip()
    if git("rev-parse", "HEAD") != expected or git("status", "--porcelain", "--untracked-files=all") or git(
            "ls-files", "--others", "--ignored", "--exclude-standard"):
        raise ReviewError("source checkout differs from the independent revision")
    entries = git("ls-tree", "-r", "-z", "HEAD").split("\0")
    files = total = 0
    for entry in entries:
        if not entry:
            continue
        metadata, name = entry.split("\t", 1)
        mode, kind, oid = metadata.split()
        relative = Path(name)
        if mode not in ("100644", "100755") or kind != "blob" or relative.is_absolute() or any(
                part in (".", "..", ".git") for part in relative.parts):
            raise ReviewError("source needs a separately supported file or Gitlink profile")
        path = Path(name)
        raw = file_bytes(path, 16 << 20)
        if hashlib.sha1(b"blob " + str(len(raw)).encode() + b"\0" + raw).hexdigest() != oid or bool(
                path.stat().st_mode & 0o111) != (mode == "100755"):
            raise ReviewError("source file differs from the selected Git tree")
        files += 1
        total += len(raw)
        if files > 4096 or total > 64 << 20:
            raise ReviewError("source tree exceeds its bound")
    return {"commit": expected, "tree": git("rev-parse", "HEAD^{tree}"),
            "trackedFilesVerified": files, "trackedBytesVerified": total,
            "goModSha256": file_digest(Path("go.mod"), 1 << 20),
            "goSumSha256": file_digest(Path("go.sum"), 8 << 20)}


def save(path, value):
    with path.open("x") as output:
        json.dump(value, output, indent=2, sort_keys=True)
        output.write("\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scanner", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--go-version", required=True)
    args = parser.parse_args()
    receipt = {"schema": 1, "observedAt": datetime.now(timezone.utc).isoformat(), "scanMode": "source",
               "patterns": ["./..."], "testPackagesIncluded": True, "scannerVersionExpected": SCANNER_VERSION,
               "analysisGoVersionExpected": args.go_version, "releaseAccepted": False}
    created = False
    phase = "output_directory"
    try:
        args.output.mkdir(mode=0o700)
        created = True
        phase = "source_binding_before"
        before = source_binding(args.source_commit)
        scanner = args.scanner.resolve(strict=True)
        scanner_sha = file_digest(scanner, 64 << 20)
        receipt.update(source=before, scannerBinarySha256=scanner_sha, scannerOriginAuthenticated=False)
        argv = [str(scanner), "-json", "-scan=symbol", "-mode=source", "-test", "-db=" + DATABASE, "./..."]
        env = dict(os.environ, GOTELEMETRY="off", GOTOOLCHAIN="local", GOWORK="off", CGO_ENABLED="0",
                   GOFLAGS="-mod=readonly", GOMAXPROCS="2", GOMEMLIMIT="1536MiB")
        phase = "scanner_capture"
        process = run_bounded(argv, args.output, env=env)
        receipt["process"] = process
        phase = "source_binding_after"
        if source_binding(args.source_commit) != before or file_digest(scanner, 64 << 20) != scanner_sha:
            raise ReviewError("source or selected scanner changed")
        if process["actualExit"] != 0 or not process["outputComplete"]:
            raise ReviewError("scanner did not complete successfully")
        raw = (args.output / "govulncheck.json").read_bytes()
        if sha(raw) != process["streams"]["stdout"]["sha256"]:
            raise ReviewError("retained output changed")
        phase = "protocol_review"
        summary = summarize(raw, go_version=args.go_version)
        summary["source"] = before
        summary["processReceiptRequiredAndVerified"] = True
        save(args.output / "review.json", summary)
        receipt["status"] = summary["status"]
        result = 1 if summary["uniqueFindingAdvisories"] else 0
    except (ReviewError, OSError, ValueError, RecursionError, subprocess.SubprocessError) as error:
        receipt.update(status="SCAN_FAILED_OR_INCOMPLETE", errorType=type(error).__name__, failureStage=phase)
        result = 2
    if created:
        try:
            save(args.output / "execution.json", receipt)
        except FileExistsError:
            result = 2
    print(json.dumps({"status": receipt.get("status"), "exit": result, "releaseAccepted": False}))
    return result


if __name__ == "__main__":
    sys.exit(main())
