#!/usr/bin/env python3
"""Stage the public installer from one exact published stable release."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

REPOSITORY = "777genius/agent-notifications"


def api(path):
    return json.loads(subprocess.check_output(["gh", "api", path]))


def stage(destination):
    tag = api(f"repos/{REPOSITORY}/releases/latest")["tag_name"]
    if not re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", tag):
        raise ValueError("latest release must have a stable version tag")
    commit = api(f"repos/{REPOSITORY}/commits/{tag}")["sha"]
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("invalid release commit")
    entry = api(f"repos/{REPOSITORY}/contents/bin/setup.sh?ref={commit}")
    if entry.get("encoding") != "base64" or entry.get("type") != "file":
        raise ValueError("invalid release loader entry")
    body = base64.b64decode("".join(entry["content"].splitlines()), validate=True)
    if not body.startswith(b"#!/usr/bin/env bash\n") or b"\0" in body or len(body) > 262144:
        raise ValueError("invalid release loader")
    subprocess.run(["bash", "-n"], input=body, check=True)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=destination.parent, prefix=".release-loader-", delete=False) as output:
            temporary = Path(output.name)
            output.write(body)
        temporary.chmod(0o644)
        os.replace(temporary, destination)
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
    print(f"Public installer staged from {tag} at {commit}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    stage(parser.parse_args().destination)
