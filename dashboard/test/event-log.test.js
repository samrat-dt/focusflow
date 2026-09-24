import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { uuidv7 } from '../lib/uuidv7.js';
import { makeEvent, appendEvent, readEvents } from '../lib/event-log.js';

const tmp = () => fs.mkdtempSync(path.join(os.tmpdir(), 'ff-events-'));

test('uuidv7 is well formed and strictly increasing', () => {
  const ids = Array.from({ length: 5000 }, () => uuidv7());
  for (const id of ids) assert.match(id, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  for (let i = 1; i < ids.length; i++) assert.ok(ids[i] > ids[i - 1], 'ids sort in creation order');
  assert.equal(new Set(ids).size, ids.length);
});

test('makeEvent fills v, id, both clocks, device', () => {
  const e = makeEvent({ device: 'dev1', kind: 'task.appeared', subject: 'task:a', payload: { x: 1 } });
  assert.equal(e.v, 1);
  assert.equal(e.device, 'dev1');
  assert.equal(e.kind, 'task.appeared');
  assert.ok(e.id && e.recorded_at && e.event_time);
  assert.equal(e.event_time, e.recorded_at);
});

test('appendEvent writes one line per event under events/YYYY-MM/<device>.jsonl', () => {
  const dir = tmp();
  const e = makeEvent({ device: 'dev1', kind: 'instance.created', subject: 'instance', payload: {}, recordedAt: '2026-09-22T10:00:00.000Z' });
  const file = appendEvent(dir, e);
  assert.equal(file, path.join(dir, 'events', '2026-09', 'dev1.jsonl'));
  appendEvent(dir, makeEvent({ device: 'dev1', kind: 'task.appeared', subject: 'task:a', payload: {}, recordedAt: '2026-09-22T10:01:00.000Z' }));
  const lines = fs.readFileSync(file, 'utf8').trimEnd().split('\n');
  assert.equal(lines.length, 2);
  assert.equal(JSON.parse(lines[0]).kind, 'instance.created');
});

test('readEvents merges devices and months in (recorded_at, id) order', () => {
  const dir = tmp();
  appendEvent(dir, makeEvent({ device: 'b', kind: 'k', subject: 's', payload: { n: 2 }, recordedAt: '2026-10-01T00:00:00.000Z' }));
  appendEvent(dir, makeEvent({ device: 'a', kind: 'k', subject: 's', payload: { n: 1 }, recordedAt: '2026-09-30T23:00:00.000Z' }));
  appendEvent(dir, makeEvent({ device: 'a', kind: 'k', subject: 's', payload: { n: 3 }, recordedAt: '2026-10-02T00:00:00.000Z' }));
  const { events, malformed } = readEvents(dir);
  assert.deepEqual(events.map(e => e.payload.n), [1, 2, 3]);
  assert.deepEqual(malformed, []);
});

test('readEvents skips a truncated final line and reports it, without rewriting the file', () => {
  const dir = tmp();
  const file = appendEvent(dir, makeEvent({ device: 'a', kind: 'k', subject: 's', payload: { n: 1 }, recordedAt: '2026-09-22T10:00:00.000Z' }));
  fs.appendFileSync(file, '{"v":1,"id":"trunc');
  const before = fs.readFileSync(file, 'utf8');
  const { events, malformed } = readEvents(dir);
  assert.equal(events.length, 1);
  assert.equal(malformed.length, 1);
  assert.equal(malformed[0].file, file);
  assert.equal(malformed[0].line, 2);
  assert.equal(fs.readFileSync(file, 'utf8'), before, 'reader never rewrites');
});

test('appendEvent after a truncated line starts on a fresh line', () => {
  const dir = tmp();
  const file = appendEvent(dir, makeEvent({ device: 'a', kind: 'k', subject: 's', payload: { n: 1 }, recordedAt: '2026-09-22T10:00:00.000Z' }));
  fs.appendFileSync(file, '{"v":1,"id":"trunc');
  appendEvent(dir, makeEvent({ device: 'a', kind: 'k', subject: 's', payload: { n: 2 }, recordedAt: '2026-09-22T10:05:00.000Z' }));
  const { events, malformed } = readEvents(dir);
  assert.deepEqual(events.map(e => e.payload.n), [1, 2]);
  assert.equal(malformed.length, 1);
});

test('readEvents treats parseable non-events as malformed', () => {
  const dir = tmp();
  const file = appendEvent(dir, makeEvent({ device: 'a', kind: 'k', subject: 's', payload: {}, recordedAt: '2026-09-22T10:00:00.000Z' }));
  fs.appendFileSync(file, '[1,2,3]\n{"hello":"world"}\n');
  const { events, malformed } = readEvents(dir);
  assert.equal(events.length, 1);
  assert.equal(malformed.length, 2);
});

test('readEvents on a missing data dir returns nothing', () => {
  const { events, malformed } = readEvents(path.join(tmp(), 'nope'));
  assert.deepEqual(events, []);
  assert.deepEqual(malformed, []);
});
