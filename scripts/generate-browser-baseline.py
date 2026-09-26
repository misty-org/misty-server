#!/usr/bin/env python3
"""Generate a clean baseline from an EMPTY upgraded pg_dump --schema-only.

Never use a customer data dump. Exclude goose_db_version and its sequence.
The generated baseline includes neutral forward upgrades for existing installs.
Review and run both fresh and upgrade contracts before replacing a baseline.
"""
from pathlib import Path
import argparse
import re
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--schema', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
args=parser.parse_args()
upgrades=Path(__file__).resolve().parents[1]/'internal/platform/postgres/upgrades'
schema=args.schema.read_text()
lines=[];grants=[]
for line in schema.splitlines():
 if line.startswith('--') or line.startswith('\\') or line.startswith('SET ') or line.startswith("SELECT pg_catalog.set_config"):continue
 if line.startswith('ALTER DEFAULT PRIVILEGES FOR ROLE postgres'):line=line.replace(' FOR ROLE postgres','')
 if line.startswith(('GRANT ','REVOKE ','ALTER DEFAULT PRIVILEGES')):
  grants.append(line);continue
 lines.append(line)
schema='\n'.join(lines).replace('CREATE SCHEMA public;','CREATE SCHEMA IF NOT EXISTS public;')
schema=re.sub(r'\n{3,}','\n\n',schema).strip()
assert 'misty_archive.' not in schema
body='''-- Browser workspace baseline. Generated from an empty, upgraded PostgreSQL 16 database.
-- Fresh installs do not execute historical pricing or retired billing migrations.
-- Existing installations at 20270215120000 or later upgrade in place without
-- rebuilding tables. Earlier installations require the archived operator upgrade.
-- +goose Up
-- +goose StatementBegin
DO $misty_migration$
DECLARE previous_version bigint;
BEGIN
 PERFORM set_config('app.rls_mode','service',true);
 IF to_regclass('public.users') IS NULL THEN
  IF EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public' AND tablename<>'goose_db_version') THEN
   RAISE EXCEPTION 'Refusing baseline over an unrecognized partial database';
  END IF;
  CREATE EXTENSION IF NOT EXISTS vector;
  PERFORM set_config('check_function_bodies','off',true);
  EXECUTE $misty_schema$
'''+schema+'''
$misty_schema$;
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='misty_app') THEN
  EXECUTE $misty_grants$
'''+ '\n'.join(grants)+'''
$misty_grants$;
 END IF;
 INSERT INTO public.ai_feature_flags(surface_id,action_id,model_id,enabled,rollout_percent) VALUES('*','*','*',TRUE,100);
 ELSE
  SELECT COALESCE(MAX(version_id),0) INTO previous_version FROM public.goose_db_version WHERE is_applied;
  IF previous_version < 20270215120000 THEN
   RAISE EXCEPTION 'Upgrade this historical database to 20270215120000 with the archived operator migrations before applying the browser baseline';
  END IF;
'''
for p in sorted(upgrades.glob('*.sql')):
 version=int(p.name.split('_')[0]);up=p.read_text().split('-- +goose Up',1)[1].split('-- +goose Down',1)[0]
 up='\n'.join(line for line in up.splitlines() if not line.startswith('-- +goose'))
 body+=f' IF previous_version < {version} THEN\n  EXECUTE $misty_upgrade$\n{up}\n$misty_upgrade$;\n END IF;\n'
body+=''' END IF;
END $misty_migration$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Browser baseline is forward-only; use the verified backup for operator recovery'; END $$;
-- +goose StatementEnd
'''
args.output.write_text(body)
print(f'Wrote {len(body)} byte baseline to {args.output}')
