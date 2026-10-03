import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createDisplayContext, cleanText, type Fact } from './display.ts';

const fact = (sessionID = 's', requestID = 'r'): Fact => ({
  version: 1, kind: 'question_asked', rootSession: true, sessionID, requestID,
});
const asked = (sessionID = 's', id = 'r', question = 'Use the new installer?') => ({
  type: 'question.asked', properties: { sessionID, id, questions: [{ question, header: 'PRIVATE HEADER', options: ['PRIVATE OPTION'] }] },
});

// Regression: absent title lookup, header-derived questions, mutation after
// arrival, or a cached rename would change the actual desktop display payload.
test('immutable concrete questions and fresh exact-session titles', async () => {
  let title = 'Installer work';
  const ids: string[] = [];
  const display = createDisplayContext({ session: { get: async ({ path }) => {
    ids.push(path.id); return { data: { id: path.id, title } };
  } } });
  const native = asked();
  const snapshot = display.capture(native);
  native.properties.questions[0]!.question = 'MUTATED AFTER ARRIVAL';
  const first = await display.enrich(fact());
  assert.deepEqual(first.display, { sessionID: 's', requestID: 'r', sessionTitle: 'Installer work', question: 'Use the new installer?' });
  title = 'Renamed work';
  assert.equal((await display.enrich({ ...fact(), kind: 'turn_idle_verified', requestID: undefined })).display?.sessionTitle, title);
  assert.deepEqual(ids, ['s', 's']);
  display.release(snapshot);
  assert.equal((await display.enrich(fact())).display?.question, undefined);
});

// Regression: interleaving sessions, resolution or a new turn could attach an
// unrelated/private stale question to a verified neutral notification.
test('cross-session and resolved snapshots cannot leak through awaited title lookups', async () => {
  let resolve!: (value: unknown) => void;
  const display = createDisplayContext({ session: { get: () => new Promise((done) => { resolve = done; }) } });
  display.capture(asked('other', 'r', 'OTHER SESSION SECRET'));
  display.capture(asked());
  const pending = display.enrich(fact());
  await Promise.resolve();
  display.capture({ type: 'question.replied', properties: { sessionID: 's', requestID: 'r' } });
  resolve({ data: { id: 'other', title: 'OTHER TITLE SECRET' } });
  assert.equal((await pending).display, undefined);
});

test('identical request IDs in different sessions retain their own question text', async () => {
  const display = createDisplayContext({ session: { get: async ({ path }) => ({ id: path.id, title: '' }) } });
  display.capture(asked('first', 'r', 'First question?'));
  display.capture(asked('second', 'r', 'Second question?'));
  assert.equal((await display.enrich(fact('first'))).display?.question, 'First question?');
  assert.equal((await display.enrich(fact('second'))).display?.question, 'Second question?');
});

test('a new turn invalidates pending context; repeat updates of the same turn do not', async () => {
  const display = createDisplayContext({ session: { get: async () => ({ id: 's', title: '' }) } });
  const user = (id: string) => ({ type: 'message.updated', properties: { info: { role: 'user', sessionID: 's', id } } });
  display.capture(user('u1'));
  display.capture(asked());
  display.capture(user('u1'));
  assert.equal((await display.enrich(fact())).display?.question, 'Use the new installer?');
  display.capture(user('u2'));
  assert.equal((await display.enrich(fact())).display, undefined);
  display.capture(asked('s', 'r2', 'Current question?'));
  display.capture(user('u1'));
  assert.equal((await display.enrich(fact('s', 'r2'))).display?.question, 'Current question?');
});

test('concurrent duplicates cannot overwrite the question; multiple actual questions retain order', async () => {
  const display = createDisplayContext({ session: { get: async () => ({ id: 's', title: '' }) } });
  const native = asked();
  native.properties.questions.push({ question: 'Restart OpenCode?', header: 'HEADER', options: [] });
  display.capture(native);
  display.capture(asked('s', 'r', 'DUPLICATE REPLACEMENT'));
  assert.equal((await display.enrich(fact())).display?.question, 'Use the new installer? · Restart OpenCode?');
});

// Regression: the SDK can admit a duplicate while the first observation loses
// admission and returns. Releasing the loser during the winner's title await
// must not clear the active request's original concrete question.
test('a losing duplicate observation cannot release the winning emit snapshot', async () => {
  let resolve!: (value: unknown) => void;
  const display = createDisplayContext({ session: { get: () => new Promise((done) => { resolve = done; }) } });
  const first = display.capture(asked());
  const duplicate = display.capture(asked('s', 'r', 'DUPLICATE REPLACEMENT'));
  const winningEmit = display.enrich(fact());
  await Promise.resolve();
  display.release(first);
  resolve({ id: 's', title: 'Installer work' });
  assert.deepEqual((await winningEmit).display, {
    sessionID: 's', requestID: 'r', sessionTitle: 'Installer work', question: 'Use the new installer?',
  });
  display.release(duplicate);
  const afterAllObservers = display.enrich(fact());
  await Promise.resolve();
  resolve({ id: 's', title: 'Installer work' });
  assert.equal((await afterAllObservers).display?.question, undefined);
});

// A shared lifetime must not override native resolution: both observations
// can still be in flight when the user answers/rejects the request.
test('resolution invalidates a duplicate-held snapshot during delayed enrichment', async () => {
  let resolve!: (value: unknown) => void;
  const display = createDisplayContext({ session: { get: () => new Promise((done) => { resolve = done; }) } });
  const first = display.capture(asked());
  const duplicate = display.capture(asked());
  const winningEmit = display.enrich(fact());
  await Promise.resolve();
  display.release(first);
  display.capture({ type: 'question.rejected', properties: { sessionID: 's', requestID: 'r' } });
  resolve({ id: 's', title: 'Installer work' });
  assert.equal((await winningEmit).display?.question, undefined);
  display.release(duplicate);
});

test('bounded metadata failure preserves the neutral fact and never invents header text', async () => {
  const display = createDisplayContext({ session: { get: async () => { throw new Error('PRIVATE ERROR'); } } });
  display.capture(asked('s', 'r', ''));
  assert.deepEqual(await display.enrich(fact()), fact());
  for (const invalid of ['\u0000bad', '\ud800', 'x'.repeat(1025)]) assert.equal(cleanText(invalid, 1024), '');
  assert.equal(cleanText('  Hello\n世界 🚀  ', 1024), 'Hello 世界 🚀');
});

// Regression: ignored aborts could grow native requests without bound or block
// generic notifications indefinitely. Capacity stays occupied until settlement.
test('lookup timeout and concurrency saturation fall back without additional native requests', async () => {
  let calls = 0;
  const display = createDisplayContext({ session: { get: () => {
    calls++; return new Promise(() => {});
  } } }, 5, 1);
  const pending = display.enrich({ ...fact(), kind: 'turn_idle_verified' });
  await Promise.resolve();
  assert.deepEqual(await display.enrich(fact('other')), fact('other'));
  assert.equal((await pending).display, undefined);
  assert.deepEqual(await display.enrich(fact()), fact());
  assert.equal(calls, 1);
});
