#!/usr/bin/env python3
"""Prepare a separate, audited public-history candidate; never modify the source.

Requires Go, git-filter-repo and gitleaks. Keeps commit identities/dates (including
empty commits); snapshots current nonignored server files after history filtering.
All original refs and a commit map remain in the external evidence directory.
No remote is configured and nothing is pushed. Inspect the candidate before use.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
import tarfile


def run(*args, cwd=None):
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=Path, help='new directory outside the source repository')
    parser.add_argument('--redactions-file', type=Path, help='private JSON array of historical credentials to redact')
    parser.add_argument('--snapshot-workspace', action='store_true', help='restore the complete committed public workspace after filtering; requires a clean source')
    args = parser.parse_args()
    source = Path(run('git', 'rev-parse', '--show-toplevel')).resolve()
    if args.snapshot_workspace and run('git', 'status', '--porcelain', cwd=source):
        parser.error('--snapshot-workspace requires all intended source changes to be committed')
    output = args.output.resolve()
    if output.exists() or output.is_relative_to(source):
        parser.error('output must be a new directory outside the repository')
    output.mkdir(parents=True, mode=0o700)
    refs = run('git', 'for-each-ref', '--format=%(refname) %(objectname)', cwd=source)
    (output / 'original-refs.txt').write_text(refs + '\n')
    # Bundle is private rollback evidence, never part of the candidate repository.
    subprocess.run(['git', 'bundle', 'create', str(output / 'original-private.bundle'), '--all'], cwd=source, check=True, stdout=subprocess.DEVNULL)
    candidate = output / 'candidate.git'
    subprocess.run(['git', 'clone', '--quiet', '--mirror', '--no-local', str(source), str(candidate)], check=True)
    run('git', 'remote', 'remove', 'origin', cwd=candidate)
    # Codex checkpoints and private rollback refs can point to trees rather
    # than commits. Keep them only in the private source bundle.
    for ref in run('git', 'for-each-ref', '--format=%(refname)', cwd=candidate).splitlines():
        if not ref.startswith(('refs/heads/', 'refs/tags/', 'refs/remotes/')):
            run('git', 'update-ref', '-d', ref, cwd=candidate)
    # Apply to every publication ref, not only the current branch. Entire versions of mixed
    # files are omitted conservatively. Reviewed current source restores them.
    callback = output / 'filter.py'
    callback.write_text('import json\nredactions = json.loads(' + repr(args.redactions_file.read_text() if args.redactions_file else '[]') + ')\n' + r'''
import re
private = (b'server/apps/payments/', b'server/packages/entitlements/', b'server/internal/billing/', b'internal/billing/', b'apps/payments/', b'packages/entitlements/', b'billing/', b'apps/api/src/modules/usage/', b'apps/api/src/modules/entitlements/')
backend = filename.startswith((b'server/', b'internal/', b'test/', b'docs/', b'apps/payments/', b'packages/entitlements/', b'api/', b'billing/', b'db/', b'integration/', b'apps/api/'))
retired_policy_file = backend and re.search(rb'(?:^|/)(?:stripe[^/]*|hosted_ai[^/]*|credit_wallet[^/]*|credits[^/]*|subscriptions_(?:pro_trial|create_checkout|create_stripe)[^/]*)\.(?:go|sql|ts|json|md)$', filename)
# Old schema history is private; the current clean baseline is restored below.
old_schema = filename.startswith((b'server/internal/platform/postgres/migrations/', b'internal/platform/postgres/migrations/', b'migrations/', b'db/migrations/', b'apps/api/migrations/'))
secret_env = (re.search(rb'(?:^|/)\.env(?:[./]|$)', filename) or filename.endswith(b'.env')) and not filename.endswith((b'.example', b'.template', b'.sample'))
generated = filename.startswith((b'release/', b'releases/'))
if filename.startswith(private) or retired_policy_file or old_schema or secret_env or generated:
    return (None, mode, blob_id)
if mode == b'160000':
    return (filename, mode, blob_id)
data = value.get_contents_by_identifier(blob_id)
original = data
for secret in redactions:
    data = data.replace(secret.encode(), b'REDACTED_LEGACY_CREDENTIAL')
if data != original:
    blob_id = value.insert_file_with_contents(data)
pattern = rb'internal/billing|\bstripe\b|trial_started_at|hosted.?ai|credit_(?:ledger|reservations|purchases)|stripe-go|STRIPE_(?:SECRET|WEBHOOK|PRICE)|stripe\.com/v1|weekly_allowance_' + rb'microusd|credit_wallets|(?:weekly|monthly|trial)Allowance|(?:margin|markup)(?:BPS|Percent|Multiplier)|Microusd' + rb'PerCredit'
if backend and re.search(pattern, data, re.I):
    return (None, mode, blob_id)
# Root module manifests also contained the old billing dependency.
if filename in (b'go.mod', b'go.sum', b'package.json', b'pnpm-lock.yaml') and b'stripe' in data.lower():
    return (None, mode, blob_id)
return (filename, mode, blob_id)
''')
    with (output / 'filter.log').open('w') as log:
        subprocess.run(['git', 'filter-repo', '--force', '--prune-empty', 'never', '--prune-degenerate', 'never', '--file-info-callback', str(callback)], cwd=candidate, stdout=log, stderr=subprocess.STDOUT, check=True)
    # Keep the historical names for audit. Remotes from the source are evidence,
    # not push destinations; this bare candidate has no configured remote.
    checkout = output / 'review'
    run('git', 'worktree', 'add', '--quiet', str(checkout), 'main', cwd=candidate)
    server = checkout / 'server'
    if args.snapshot_workspace:
        # Filtering removes complete historical mixed files. Restore only the
        # reviewed current commit, including tracked fixtures ignored by glob
        # rules and public release configuration; audit the result below.
        run('git', 'rm', '-r', '-q', '--', '.', cwd=checkout)
        archive = subprocess.Popen(['git', 'archive', 'HEAD'], cwd=source, stdout=subprocess.PIPE)
        with tarfile.open(fileobj=archive.stdout, mode='r|') as contents:
            contents.extractall(checkout, filter='data')
        archive.stdout.close()
        if archive.wait() != 0:
            raise ValueError('cannot export committed workspace')
        run('git', 'add', '--force', '--all', '--', '.', cwd=checkout)
        for row in run('git', 'ls-tree', '-r', 'HEAD', cwd=source).splitlines():
            metadata, path = row.split('\t', 1)
            mode, kind, oid = metadata.split()
            if mode == '160000':
                run('git', 'update-index', '--add', '--cacheinfo', mode + ',' + oid + ',' + path, cwd=checkout)
        if run('git', 'write-tree', cwd=checkout) != run('git', 'rev-parse', 'HEAD^{tree}', cwd=source):
            raise ValueError('public workspace snapshot differs from the reviewed source')
        count = len(run('git', 'ls-files', '--', 'server', cwd=checkout).splitlines())
        run('git', 'commit', '-qm', 'Restore reviewed browser workspace after history sanitation', cwd=checkout)
    else:
        if server.exists():
            shutil.rmtree(server)
        files = run('git', 'ls-files', '--cached', '--others', '--exclude-standard', '--', 'server', cwd=source).splitlines()
        count = 0
        for relative in sorted(set(files)):
            src = source / relative
            if not src.is_file():
                continue
            if src.is_symlink():
                raise ValueError('review symbolic links before import: ' + relative)
            dst = checkout / relative
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)
            count += 1
        run('git', 'add', '-A', '--', 'server', cwd=checkout)
        run('git', 'commit', '-qm', 'Import current browser server with optional external billing adapter', cwd=checkout)
    # A source-only candidate must build without ignored developer files or a
    # private checkout. In particular, broad monorepo ignore rules must not hide
    # newly added Go configuration implementations.
    with (output / 'server-build.log').open('w') as log:
        subprocess.run(['go', 'build', '-o', str(output / 'misty-server'), './cmd/misty-server'], cwd=server, stdout=log, stderr=subprocess.STDOUT, check=True)
    guard = source / 'server/scripts/check-public-history.py'
    # Include source remote-tracking refs too: an old remote parent is a leak.
    audit_refs = run('git', 'for-each-ref', '--format=%(refname)', cwd=candidate).splitlines()
    with (output / 'policy-audit.log').open('w') as log:
        subprocess.run(['python3', str(guard), *audit_refs], cwd=candidate, stdout=log, stderr=subprocess.STDOUT, check=True)
    with (output / 'secret-audit.log').open('w') as log:
        subprocess.run(['gitleaks', 'git', '--config', str(source / 'server/.gitleaks.toml'), '--redact', '--no-banner', '--log-opts=--all', '--report-format=json', '--report-path=' + str(output / 'secret-audit.json'), str(candidate)], stdout=log, stderr=subprocess.STDOUT, check=True)
    # Filtering must retain authors, committers and original timestamp bytes.
    mapping = candidate / 'filter-repo/commit-map'
    checked = 0
    for line in mapping.read_text().splitlines()[1:]:
        old, new = line.split()
        if new == '0' * 40:
            raise ValueError('a contribution commit was dropped')
        fmt = '%an%x00%ae%x00%at%x00%ai%x00%cn%x00%ce%x00%ct%x00%ci'
        if run('git', 'show', '-s', '--format=' + fmt, old, cwd=source) != run('git', 'show', '-s', '--format=' + fmt, new, cwd=candidate):
            raise ValueError('contribution identity changed: ' + old)
        checked += 1
    report = {'source': str(source), 'candidate': str(candidate), 'server_files': count, 'server_build_verified': True, 'workspace_snapshot_verified': args.snapshot_workspace, 'contributions_verified': checked, 'refs_audited': len(audit_refs), 'remote_configured': False, 'pushed': False}
    (output / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
