-- +goose Up
-- Agent tools take the agent vocabulary (docs/agents/harness.md): dotted, singular names
-- that never use UI labels. No aliases are kept, so stored references move with them: the
-- action journal, device job capabilities and the capability lists on grants.
CREATE TEMP TABLE tool_renames(old_name text PRIMARY KEY, new_name text NOT NULL);
INSERT INTO tool_renames VALUES
    ('spaces.list', 'space.list'),
    ('spaces.manage', 'space.manage'),
    ('members.list', 'space.member.list'),
    ('members.resolve', 'space.member.resolve'),
    ('notes.create', 'note.create'),
    ('notes.read', 'note.read'),
    ('notes.search', 'note.search'),
    ('notes.tags', 'note.tag'),
    ('notes.update', 'note.update'),
    ('tasks.create', 'task.create'),
    ('tasks.query', 'task.query'),
    ('tasks.update', 'task.update'),
    ('tasks.update_assigned', 'task.update_assigned'),
    ('calendar.create', 'event.create'),
    ('calendar.query', 'event.query'),
    ('calendar.update', 'event.update'),
    ('drawings.apply', 'drawing.apply'),
    ('drawings.create', 'drawing.create'),
    ('drawings.list', 'drawing.list'),
    ('drawings.read', 'drawing.read'),
    ('roadmaps.canvas', 'roadmap.canvas'),
    ('roadmaps.create', 'roadmap.create'),
    ('roadmaps.plan', 'roadmap.plan'),
    ('roadmaps.query', 'roadmap.query'),
    ('roadmaps.read', 'roadmap.read'),
    ('roadmaps.update', 'roadmap.update'),
    ('messages.search', 'message.search'),
    ('messages.send', 'message.send'),
    ('threads.create', 'thread.create'),
    ('threads.list', 'thread.list'),
    ('threads.post', 'thread.post'),
    ('threads.read', 'thread.read'),
    ('library.albums', 'file.album.list'),
    ('library.organize', 'file.organize'),
    ('library.promote_attachment', 'file.promote_attachment'),
    ('library.read', 'file.read'),
    ('library.search', 'file.search'),
    ('library.update', 'file.update'),
    ('memory.list', 'memory.pref.list'),
    ('memory.remember', 'memory.pref.remember'),
    ('memory.update', 'memory.pref.update'),
    ('memory.forget', 'memory.pref.forget'),
    ('recall.search', 'memory.history.search'),
    ('recall.timeline', 'memory.history.timeline'),
    ('recall.forget', 'memory.history.forget'),
    ('notebook.read', 'log.work.read'),
    ('notebook.write', 'log.work.write'),
    ('checklist.update', 'log.work.checklist'),
    ('agents.delegate', 'agent.delegate'),
    ('agents.wait', 'agent.wait'),
    ('agents.list', 'agent.list'),
    ('agents.configure', 'agent.configure'),
    ('agents.directory', 'agent.directory'),
    ('agents.request', 'agent.request'),
    ('agents.request_status', 'agent.request_status'),
    ('ask.delegate', 'agent.background'),
    ('apps.connect', 'integration.connect'),
    ('apps.connected', 'integration.connected'),
    ('apps.execute', 'integration.execute'),
    ('apps.schemas', 'integration.schemas'),
    ('apps.search', 'integration.search'),
    ('files.list', 'folder.list'),
    ('files.read', 'folder.read'),
    ('files.write', 'folder.write'),
    ('files.search', 'folder.search'),
    ('files.copy', 'folder.copy'),
    ('files.move', 'folder.move'),
    ('files.delete', 'folder.delete'),
    ('files.send', 'folder.send'),
    ('tabs.list', 'browser.tab.list'),
    ('tabs.open', 'browser.tab.open'),
    ('bookmarks.list', 'bookmark.list'),
    ('bookmarks.add', 'bookmark.add'),
    ('attached_files.read', 'attached_file.read'),
    ('items.rename', 'item.rename'),
    ('items.delete', 'item.delete'),
    ('workflows.schedule', 'memory.workflow.schedule'),
    ('workflows.unschedule', 'memory.workflow.unschedule'),
    ('misty.finish.task', 'agent.finish'),
    ('context.get', 'agent.context'),
    ('devices.list', 'device.list'),
    ('methods.list', 'method.list'),
    ('methods.save', 'method.save'),
    ('methods.use', 'method.use');

UPDATE agent_toolbox_action_journal j SET tool_name = r.new_name FROM tool_renames r WHERE j.tool_name = r.old_name;
UPDATE workflow_device_node_jobs j SET required_capability = r.new_name FROM tool_renames r WHERE j.required_capability = r.old_name;
UPDATE ai_invocations i SET device_wait_capability = r.new_name FROM tool_renames r WHERE i.device_wait_capability = r.old_name;
UPDATE space_runs s SET device_wait_capability = r.new_name FROM tool_renames r WHERE s.device_wait_capability = r.old_name;

-- Capability lists on device and run grants are JSON arrays of names.
UPDATE ai_invocation_contexts c SET capabilities = (
    SELECT jsonb_agg(COALESCE(r.new_name, item.value) ORDER BY item.ordinality)
    FROM jsonb_array_elements_text(c.capabilities) WITH ORDINALITY AS item(value, ordinality)
    LEFT JOIN tool_renames r ON r.old_name = item.value)
WHERE jsonb_typeof(c.capabilities) = 'array' AND EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(c.capabilities) value JOIN tool_renames r ON r.old_name = value);
UPDATE agent_run_contexts c SET capabilities = (
    SELECT jsonb_agg(COALESCE(r.new_name, item.value) ORDER BY item.ordinality)
    FROM jsonb_array_elements_text(c.capabilities) WITH ORDINALITY AS item(value, ordinality)
    LEFT JOIN tool_renames r ON r.old_name = item.value)
WHERE jsonb_typeof(c.capabilities) = 'array' AND EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(c.capabilities) value JOIN tool_renames r ON r.old_name = value);
UPDATE trusted_devices c SET capabilities = (
    SELECT jsonb_agg(COALESCE(r.new_name, item.value) ORDER BY item.ordinality)
    FROM jsonb_array_elements_text(c.capabilities) WITH ORDINALITY AS item(value, ordinality)
    LEFT JOIN tool_renames r ON r.old_name = item.value)
WHERE jsonb_typeof(c.capabilities) = 'array' AND EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(c.capabilities) value JOIN tool_renames r ON r.old_name = value);
DROP TABLE tool_renames;

-- +goose Down
CREATE TEMP TABLE tool_renames(old_name text PRIMARY KEY, new_name text NOT NULL);
INSERT INTO tool_renames VALUES
    ('space.list', 'spaces.list'),
    ('space.manage', 'spaces.manage'),
    ('space.member.list', 'members.list'),
    ('space.member.resolve', 'members.resolve'),
    ('note.create', 'notes.create'),
    ('note.read', 'notes.read'),
    ('note.search', 'notes.search'),
    ('note.tag', 'notes.tags'),
    ('note.update', 'notes.update'),
    ('task.create', 'tasks.create'),
    ('task.query', 'tasks.query'),
    ('task.update', 'tasks.update'),
    ('task.update_assigned', 'tasks.update_assigned'),
    ('event.create', 'calendar.create'),
    ('event.query', 'calendar.query'),
    ('event.update', 'calendar.update'),
    ('drawing.apply', 'drawings.apply'),
    ('drawing.create', 'drawings.create'),
    ('drawing.list', 'drawings.list'),
    ('drawing.read', 'drawings.read'),
    ('roadmap.canvas', 'roadmaps.canvas'),
    ('roadmap.create', 'roadmaps.create'),
    ('roadmap.plan', 'roadmaps.plan'),
    ('roadmap.query', 'roadmaps.query'),
    ('roadmap.read', 'roadmaps.read'),
    ('roadmap.update', 'roadmaps.update'),
    ('message.search', 'messages.search'),
    ('message.send', 'messages.send'),
    ('thread.create', 'threads.create'),
    ('thread.list', 'threads.list'),
    ('thread.post', 'threads.post'),
    ('thread.read', 'threads.read'),
    ('file.album.list', 'library.albums'),
    ('file.organize', 'library.organize'),
    ('file.promote_attachment', 'library.promote_attachment'),
    ('file.read', 'library.read'),
    ('file.search', 'library.search'),
    ('file.update', 'library.update'),
    ('memory.pref.list', 'memory.list'),
    ('memory.pref.remember', 'memory.remember'),
    ('memory.pref.update', 'memory.update'),
    ('memory.pref.forget', 'memory.forget'),
    ('memory.history.search', 'recall.search'),
    ('memory.history.timeline', 'recall.timeline'),
    ('memory.history.forget', 'recall.forget'),
    ('log.work.read', 'notebook.read'),
    ('log.work.write', 'notebook.write'),
    ('log.work.checklist', 'checklist.update'),
    ('agent.delegate', 'agents.delegate'),
    ('agent.wait', 'agents.wait'),
    ('agent.list', 'agents.list'),
    ('agent.configure', 'agents.configure'),
    ('agent.directory', 'agents.directory'),
    ('agent.request', 'agents.request'),
    ('agent.request_status', 'agents.request_status'),
    ('agent.background', 'ask.delegate'),
    ('integration.connect', 'apps.connect'),
    ('integration.connected', 'apps.connected'),
    ('integration.execute', 'apps.execute'),
    ('integration.schemas', 'apps.schemas'),
    ('integration.search', 'apps.search'),
    ('folder.list', 'files.list'),
    ('folder.read', 'files.read'),
    ('folder.write', 'files.write'),
    ('folder.search', 'files.search'),
    ('folder.copy', 'files.copy'),
    ('folder.move', 'files.move'),
    ('folder.delete', 'files.delete'),
    ('folder.send', 'files.send'),
    ('browser.tab.list', 'tabs.list'),
    ('browser.tab.open', 'tabs.open'),
    ('bookmark.list', 'bookmarks.list'),
    ('bookmark.add', 'bookmarks.add'),
    ('attached_file.read', 'attached_files.read'),
    ('item.rename', 'items.rename'),
    ('item.delete', 'items.delete'),
    ('memory.workflow.schedule', 'workflows.schedule'),
    ('memory.workflow.unschedule', 'workflows.unschedule'),
    ('agent.finish', 'misty.finish.task'),
    ('agent.context', 'context.get'),
    ('device.list', 'devices.list'),
    ('method.list', 'methods.list'),
    ('method.save', 'methods.save'),
    ('method.use', 'methods.use');

UPDATE agent_toolbox_action_journal j SET tool_name = r.new_name FROM tool_renames r WHERE j.tool_name = r.old_name;
UPDATE workflow_device_node_jobs j SET required_capability = r.new_name FROM tool_renames r WHERE j.required_capability = r.old_name;
UPDATE ai_invocations i SET device_wait_capability = r.new_name FROM tool_renames r WHERE i.device_wait_capability = r.old_name;
UPDATE space_runs s SET device_wait_capability = r.new_name FROM tool_renames r WHERE s.device_wait_capability = r.old_name;

-- Capability lists on device and run grants are JSON arrays of names.
UPDATE ai_invocation_contexts c SET capabilities = (
    SELECT jsonb_agg(COALESCE(r.new_name, item.value) ORDER BY item.ordinality)
    FROM jsonb_array_elements_text(c.capabilities) WITH ORDINALITY AS item(value, ordinality)
    LEFT JOIN tool_renames r ON r.old_name = item.value)
WHERE jsonb_typeof(c.capabilities) = 'array' AND EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(c.capabilities) value JOIN tool_renames r ON r.old_name = value);
UPDATE agent_run_contexts c SET capabilities = (
    SELECT jsonb_agg(COALESCE(r.new_name, item.value) ORDER BY item.ordinality)
    FROM jsonb_array_elements_text(c.capabilities) WITH ORDINALITY AS item(value, ordinality)
    LEFT JOIN tool_renames r ON r.old_name = item.value)
WHERE jsonb_typeof(c.capabilities) = 'array' AND EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(c.capabilities) value JOIN tool_renames r ON r.old_name = value);
UPDATE trusted_devices c SET capabilities = (
    SELECT jsonb_agg(COALESCE(r.new_name, item.value) ORDER BY item.ordinality)
    FROM jsonb_array_elements_text(c.capabilities) WITH ORDINALITY AS item(value, ordinality)
    LEFT JOIN tool_renames r ON r.old_name = item.value)
WHERE jsonb_typeof(c.capabilities) = 'array' AND EXISTS (
    SELECT 1 FROM jsonb_array_elements_text(c.capabilities) value JOIN tool_renames r ON r.old_name = value);
DROP TABLE tool_renames;
