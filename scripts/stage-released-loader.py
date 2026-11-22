#!/usr/bin/env python3
"""Stage the public installer from an exact reviewed channel controller commit."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.error import HTTPError, URLError
from urllib.parse import quote
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def verify_published_channels(channels):
    tags = set()
    for line in channels.splitlines():
        if not line.strip() or line.startswith('#'):
            continue
        fields = line.split('\t')
        if len(fields) != 6 or not fields[2]:
            raise ValueError('Invalid platform channel row')
        tags.add(fields[2])
    if not tags:
        raise ValueError('No platform channel releases to verify')
    repository = os.environ.get('GITHUB_REPOSITORY', '')
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repository):
        raise ValueError('GITHUB_REPOSITORY must be owner/name')
    token = os.environ.get('GH_TOKEN', '')
    if not token:
        raise ValueError('GH_TOKEN is required to verify published channels')
    opener = build_opener(NoRedirect())
    for tag in sorted(tags):
        request = Request(
            f'https://api.github.com/repos/{repository}/releases/tags/{quote(tag, safe="")}',
            headers={'Accept': 'application/vnd.github+json',
                     'Authorization': 'Bearer ' + token,
                     'User-Agent': 'agent-notifications-landing',
                     'X-GitHub-Api-Version': '2022-11-28'})
        try:
            with opener.open(request, timeout=30) as response:
                release = json.load(response)
        except HTTPError as exc:
            raise ValueError(f'Cannot verify platform channel {tag}: HTTP {exc.code}') from None
        except (URLError, OSError) as exc:
            raise ValueError(f'Cannot verify platform channel {tag}: {type(exc).__name__}') from None
        except (ValueError, UnicodeError):
            raise ValueError(f'Invalid GitHub release JSON for platform channel: {tag}') from None
        published = release.get('published_at') if isinstance(release, dict) else None
        if (not isinstance(release, dict) or release.get('draft') is not False or
                not isinstance(published, str) or not published.strip()):
            raise ValueError(f'Refusing unpublished platform channel: {tag}')


def stage(destination, *, verify_published=False):
    root = Path(__file__).resolve().parent.parent
    commit = subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"]).decode().strip()
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        raise ValueError("invalid controller commit")
    subprocess.run(["git", "-C", str(root), "diff", "--exit-code", "HEAD", "--",
                    "bin/setup.sh", "bin/bootstrap.sh", "bin/release-channel.sh", "release-channels.tsv"], check=True)
    if verify_published:
        channels = subprocess.check_output(["git", "-C", str(root), "show", f"{commit}:release-channels.tsv"])
        verify_published_channels(channels.decode('utf-8'))
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
    parser.add_argument('--verify-published-channels', action='store_true',
                        help='Require every platform channel release to be published before staging')
    parser.add_argument("destination", type=Path)
    args = parser.parse_args()
    stage(args.destination, verify_published=args.verify_published_channels)
