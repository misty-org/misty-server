#!/usr/bin/env python3
"""Audit every reachable commit and blob before publishing server references.

This structural/policy guard complements, and does not replace, secret scanning.
It never edits refs. Pass explicit refs or audit all local branches and tags.
"""
import argparse
import re
import subprocess
import sys

ORIGINAL_SERVER = 'eb9afc4db82161066824125432b4ef4212580d80'
PRIVATE_ROOTS = ('server/apps/payments/', 'server/packages/entitlements/',
                 'server/internal/billing/', 'internal/billing/', 'apps/payments/', 'billing/',
                 'apps/api/src/modules/usage/', 'apps/api/src/modules/entitlements/')
# Customer-facing prices and the neutral adapter protocol are public. These
# implementation identifiers belong exclusively to the private charging service.
POLICY = re.compile(rb'github\.com/stripe/stripe-go|weekly_allowance_' rb'microusd|'
                    rb'(?:weekly|monthly|trial)Allowance|'
                    rb'(?:margin|markup)(?:BPS|Percent|Multiplier)|Microusd' rb'PerCredit')


def git(*args):
    return subprocess.run(['git', *args], capture_output=True, text=True, check=True).stdout


def audit(refs):
    commits = [git('rev-parse', '--verify', ref + '^{commit}').strip() for ref in refs]
    if not commits:
        raise ValueError('no publication references to audit')
    reachable = set(git('rev-list', *commits).splitlines())
    errors = []
    if ORIGINAL_SERVER in reachable:
        errors.append('original unsanitized server ancestry remains reachable')
    # Inspect historical paths, including deleted files and aliases for a blob.
    paths = set(git('log', '--full-history', '--format=', '--name-only', *commits).splitlines())
    private = sorted(p for p in paths if p.startswith(PRIVATE_ROOTS))
    if private:
        errors.append(f'{len(private)} private implementation paths exist in reachable history')
    objects = git('rev-list', '--objects', *commits).splitlines()
    process = subprocess.Popen(['git', 'cat-file', '--batch'], stdin=subprocess.PIPE, stdout=subprocess.PIPE)
    hits = []
    try:
        for row in objects:
            oid, _, path = row.partition(' ')
            process.stdin.write((oid + '\n').encode())
            process.stdin.flush()
            header = process.stdout.readline().split()
            if len(header) != 3:
                raise ValueError('cannot read reachable object ' + oid)
            data = process.stdout.read(int(header[2]))
            if len(data) != int(header[2]) or process.stdout.read(1) != b'\n':
                raise ValueError('incomplete reachable object ' + oid)
            if header[1] == b'blob' and POLICY.search(data):
                hits.append(oid)
    finally:
        process.stdin.close()
        process.stdout.close()
        if process.wait() != 0:
            raise ValueError('object audit failed')
    if hits:
        # Print only object IDs, never commercial source or possible secrets.
        errors.append(f'{len(hits)} reachable blobs contain private policy identifiers: ' + ', '.join(hits[:12]))
    return len(reachable), len(objects), errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('refs', nargs='*')
    args = parser.parse_args()
    try:
        refs = args.refs or git('for-each-ref', '--format=%(refname)', 'refs/heads', 'refs/tags').splitlines()
        commits, objects, errors = audit(refs)
        for error in errors:
            print('BLOCKED: ' + error, file=sys.stderr)
        if errors:
            return 1
        print(f'PASS: {len(refs)} references, {commits} commits, {objects} reachable objects')
        return 0
    except (ValueError, subprocess.CalledProcessError, OSError) as error:
        print('BLOCKED: incomplete history audit: ' + str(error), file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
