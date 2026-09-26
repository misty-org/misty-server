#!/usr/bin/env python3
"""Build a review inventory; missing static matches are never deletion authority.

Routes include exact normalized frontend/native literal matches. Dynamic URL
builders, database functions, and runtime-discovered tool calls need manual review.
"""
import json
from pathlib import Path
import re

server = Path(__file__).resolve().parents[1]
root = server.parent

def normalized(value):
    value = re.sub(r'\$?\{[^}]+\}', '{}', value).split('?')[0]
    return re.sub(r'^/(?:v1|api)(?=/)', '', value)

def domain(value):
    value = value.lower()
    if any(x in value for x in ('browser_sync', '/sync/', '/sync"', '/sync/', '/sync{')):
        return 'Sync'
    if any(x in value for x in ('agent', 'ai/', 'invocation', 'mcp', 'companion', 'capabilit')):
        return 'Agents'
    if any(x in value for x in ('/auth', '/me', '/billing', '/account', '/users', 'user_', 'auth_', 'session', 'license', 'device')):
        return 'Accounts'
    return 'Spaces'

clients = {}
for folder in ('src', 'src-tauri/src', 'cli/src'):
    for path in (root/folder).rglob('*'):
        if not path.is_file() or path.suffix not in ('.ts','.tsx','.rs') or '.test.' in path.name:
            continue
        text = path.read_text()
        for match in re.finditer(r'["`\'](/[^"`\'\n ]*)["`\']', text):
            clients.setdefault(normalized(match[1]), set()).add(str(path.relative_to(root)))
routes = []
for path in sorted((server/'internal/app').glob('server_mount*.go')):
    for line, text in enumerate(path.read_text().splitlines(),1):
        if not re.search(r'\.Router\.(?:Get|Post|Put|Delete|Patch|Method|MethodFunc|Handle|Head)\(',text):
            continue
        quoted = re.findall(r'"([^"\n]+)"',text)
        route = next((s for s in quoted if s.startswith('/')), None)
        route = normalized(route) if route else None
        matches = sorted(clients.get(route,set())) if route else []
        routes.append({'source':str(path.relative_to(root)),'line':line,'registration':text.strip(),'route_suffix':route,'domain':domain(route or text),'literal_callers':matches,'caller_review':'literal-match' if matches else 'review-dynamic-or-service-caller'})
queries = {}
for path in sorted((server/'internal').rglob('*.go')):
    if path.name.endswith('_test.go'):continue
    text = path.read_text()
    for table in set(re.findall(r'\b(?:FROM|JOIN|UPDATE|INTO)\s+(?:public\.)?([a-z_][a-z_0-9]+)\b',text)):
        queries.setdefault(table,set()).add(str(path.relative_to(root)))
baseline = next((server/'internal/platform/postgres/migrations').glob('*baseline.sql'))
schema = baseline.read_text().split('$misty_schema$;')[0]
tables=[]
for table in sorted(set(re.findall(r'^CREATE TABLE public\.(\w+)',schema,re.M))):
    users=sorted(queries.get(table,set()))
    tables.append({'table':table,'domain':domain(table),'query_sources':users,'review':'direct-query-match' if users else 'review-functions-triggers-or-dynamic-sql'})
run=(server/'internal/app/run.go').read_text()
workers=re.findall(r'WorkerFunc\(func\(ctx context.Context\) \{ (.+?) \}\)',run)
output={'schema_version':int(baseline.name.split('_')[0]),'caveat':'Static candidate inventory, not a proof of unused code. Unmatched callers require manual review before deletion.','routes':routes,'tables':tables,'workers':[{'call':w,'source':'server/internal/app/run.go','domain':domain(w)} for w in workers], 'services':{'Go API':['Accounts','Spaces','Agents','Sync'],'agent-runtime':['Agents'],'journal-collab':['Spaces'],'self-host-collab':['Spaces'],'external billing adapter':['Accounts','Agents','Spaces']}}
(server/'migration').mkdir(exist_ok=True)
(server/'migration/dependency-inventory.json').write_text(json.dumps(output,indent=2)+'\n')
print(f'{len(routes)} route registrations; {len(tables)} tables; {len(workers)} workers')
