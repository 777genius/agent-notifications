#!/usr/bin/env python3
"""Join all required atomic shards, summing overlapping CLI coverage blocks."""
from pathlib import Path

SHARDS = ('cmd-setup', 'cmd-other', 'packages', 'cursorevent')


def merge(root):
    blocks = {}
    for shard in SHARDS:
        lines = (root / ('coverage-' + shard + '.txt')).read_text().splitlines()
        if not lines or lines[0] != 'mode: atomic' or len(lines) < 2:
            raise ValueError('complete atomic coverage required: ' + shard)
        seen = set()
        for line in lines[1:]:
            location, statements, count = line.split()
            statements, count = int(statements), int(count)
            if statements < 0 or count < 0 or location in seen:
                raise ValueError('invalid or repeated coverage block: ' + shard)
            seen.add(location)
            old_statements, old_count = blocks.get(location, (statements, 0))
            if old_statements != statements:
                raise ValueError('inconsistent coverage block: ' + location)
            blocks[location] = (statements, old_count + count)
    output = 'mode: atomic\n' + ''.join(
        f'{location} {statements} {count}\n'
        for location, (statements, count) in sorted(blocks.items()))
    (root / 'coverage.txt').write_text(output)


if __name__ == '__main__':
    merge(Path.cwd())
