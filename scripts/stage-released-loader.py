#!/usr/bin/env python3
"""Stage the public installer from an exact reviewed channel controller commit."""
import argparse
import os
from pathlib import Path
import re
import subprocess
import tempfile


def stage(destination):
    root = Path(__file__).resolve().parent.parent
    commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"]).decode().strip()
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("invalid controller commit")
    subprocess.run(["git", "-C", str(root), "diff", "--exit-code", "HEAD", "--",
                    "bin/setup.sh", "bin/bootstrap.sh", "bin/release-channel.sh", "release-channels.tsv"], check=True)
    body = subprocess.check_output(["git", "-C", str(root), "show", f"{commit}:bin/setup.sh"])
    # Publishing always pins the controller and its index to one snapshot.
    marker = b'controller="${BOOTSTRAP_CONTROLLER_COMMIT:-}"'
    if body.count(marker) != 1:
        raise ValueError("loader must expose exactly one controller pin")
    body = body.replace(marker, b'controller="${BOOTSTRAP_CONTROLLER_COMMIT:-' + commit.encode() + b'}"')
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
    print(f"Public platform installer staged from controller {commit}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    stage(parser.parse_args().destination)
