#!/usr/bin/env python3
"""Verify this checkout's credential policy without printing config values."""

import json
import os
import re
import subprocess
import sys


def main():
    expected = os.environ.get("REVIEW_SOURCE_COMMIT", "")
    if not re.fullmatch(r"[0-9a-f]{40}", expected):
        raise ValueError("missing exact source revision")
    head = subprocess.run(["git", "--no-optional-locks", "rev-parse", "HEAD"],
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    if head.returncode or head.stdout.decode().strip() != expected:
        raise ValueError("checkout differs from the selected revision")
    # Inspect names only. Neither values nor raw command diagnostics are printed.
    pattern = r"^(http\..*\.extraheader|include\.path|includeif\..*\.path|core\.sshcommand|credential\..*)$"
    config = subprocess.run(["git", "config", "--local", "--name-only", "--get-regexp", pattern],
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=10)
    if config.returncode != 1 or config.stdout or config.stderr:
        raise ValueError("checkout authentication config is present or could not be inspected")
    print(json.dumps({"selectedSourceVerified": True, "localAuthenticationConfigAbsent": True,
                      "configValuesPrinted": False, "scope": "Repository-local config only"}))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, subprocess.SubprocessError):
        print(json.dumps({"status": "CHECKOUT_POLICY_NOT_VERIFIED", "configValuesPrinted": False}))
        sys.exit(1)
