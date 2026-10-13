/** Replay actual PTY recordings through xterm.js's terminal core, including its
 * real column-change reflow. The emulator is a test-only installation outside
 * this repository; it is not a product dependency. */
import {readFileSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import {resolve, dirname} from 'node:path';
import assert from 'node:assert/strict';

type Line = {translateToString(trimRight?: boolean): string};
type Buffer = {baseY: number; cursorY: number; cursorX: number; length: number; getLine(index: number): Line | undefined};
type Emulator = {cols: number; rows: number; buffer: {active: Buffer}; resize(cols: number, rows: number): void; write(data: string, callback: () => void): void; dispose(): void};
type Record = {data?: string; resize?: [number, number]; checkpoint?: string};
type Receipt = {case: string; recording: string};
type Capture = {name: string; cols: number; rows: number; cursor: [number, number]; viewport: string[]; scrollback: string[]};
const [emulatorRoot, receiptFile] = process.argv.slice(2);
assert(emulatorRoot && receiptFile, 'Usage: node installer_tui_screen_test.mts EMULATOR_ROOT PTY_RECEIPTS');
const require = createRequire(resolve(emulatorRoot, 'package.json'));
const {Terminal} = require('@xterm/headless') as {Terminal: new (options: {cols: number; rows: number; scrollback: number; allowProposedApi: boolean}) => Emulator};
const receipts: Receipt[] = JSON.parse(readFileSync(receiptFile, 'utf8'));
const results: {case: string; captures: Capture[]}[] = [];
for (const receipt of receipts) {
 const events: Record[] = readFileSync(receipt.recording, 'utf8').trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
 const initial = events.find(row => row.resize)?.resize;
 if (!initial) continue;
 const terminal = new Terminal({cols: initial[0], rows: initial[1], scrollback: 1000, allowProposedApi: true});
 const captures: Capture[] = [];
 for (const event of events) {
  if (event.resize) terminal.resize(...event.resize);
  if (event.data) await new Promise<void>(done => terminal.write(event.data!, done));
  if (event.checkpoint) {
   const buffer = terminal.buffer.active;
   const all = Array.from({length: buffer.length}, (_, i) => buffer.getLine(i)?.translateToString(true) ?? '');
   const capture: Capture = {name: event.checkpoint, cols: terminal.cols, rows: terminal.rows, cursor: [buffer.cursorX, buffer.cursorY], viewport: all.slice(buffer.baseY, buffer.baseY+terminal.rows), scrollback: all};
   captures.push(capture);
   assert(capture.cursor[0] < terminal.cols && capture.cursor[1] < terminal.rows, `${receipt.case}: cursor outside screen`);
   assert(all.join('\n').includes('TEST PREAMBLE'), `${receipt.case}: erased foreign preamble at ${event.checkpoint}`);
   if (event.checkpoint === 'second-live' || event.checkpoint.startsWith('resized-')) {
    assert(all.join('\n').includes('TEST prompt [continue]:'), `${receipt.case}: erased owner prompt`);
   }
   if (event.checkpoint === 'first-live' && ['count1', 'count16'].includes(receipt.case)) {
    const screen=capture.viewport.join('\n');
    const history=screen.slice(screen.indexOf('Recent activity'),screen.indexOf('Agent Notifications'));
    assert(history.includes('Configuration phase'), `${receipt.case}: newest action absent`);
    assert(history.includes('Registration saved') === (receipt.case==='count16'), `${receipt.case}: activity-limit had no visible effect\n${screen}`);
   }
   if (event.checkpoint==='final' && receipt.case==='cancel-shared-pending') {
    const screen=capture.viewport.join('\n');
    assert(screen.includes('Installed; restart required') && (screen.match(/Registration saved; follow-up pending/g)?.length??0)===2 && !screen.includes('Canceled'), `invented shared activation attempts\n${screen}`);
   }
   if (event.checkpoint === 'final' && receipt.case==='cancel-second') {
    const screen=capture.viewport.join('\n');
    assert(screen.includes('Installed; restart required') && screen.includes('Registration saved; follow-up pending') && screen.includes('Not started') && !screen.includes('Canceled'), `cancellation final state missing\n${screen}`);
   }
   if (['normal', 'narrow50', 'wide', 'resize', 'resize-static', 'expanded', 'count1', 'count16'].includes(receipt.case) && (event.checkpoint.endsWith('live') || event.checkpoint.startsWith('resized-'))) {
    const screen = capture.viewport.join('\n');
    for (const client of ['Claude Code', 'Codex CLI', 'Gemini CLI']) assert(screen.includes(client), `${receipt.case}/${event.checkpoint}: omitted ${client}\n${screen}`);
    const statusAt = screen.indexOf('Agent Notifications');
    assert(statusAt >= 0, `${receipt.case}/${event.checkpoint}: status missing\n${screen}`);
    const activityAt = screen.indexOf('Recent activity');
    if (event.checkpoint === 'first-live' && terminal.rows >= 24) assert(activityAt >= 0 && activityAt < statusAt, `${receipt.case}: history not above status`);
    const nonblank = capture.viewport.filter(line => line.trim());
    assert(nonblank.at(-1)?.includes('╯') || nonblank.at(-1)?.includes('Gemini CLI') || nonblank.at(-1)?.includes('Not started'), `${receipt.case}: status is not last live block\n${screen}`);
    if (event.checkpoint === 'second-live') {
     if (receipt.case==='normal') assert(screen.includes('Completed 1; active 1; queued 1'), `stage counters diverged\n${screen}`);
     assert(screen.includes('Installed; restart required'), `${receipt.case}: completed first client lost`);
     assert(screen.split('Registration saved').length <= 2, `${receipt.case}: duplicated replay history`);
    }
   }
  }
 }
 writeFileSync(resolve(dirname(receipt.recording), 'screens.json'), JSON.stringify(captures, null, 2)+'\n');
 results.push({case: receipt.case, captures});
 terminal.dispose();
}
assert(results.some(row => row.case === 'resize-static' && row.captures.some(c => c.name === 'resized-32')), 'Missing nonanimated resize recording');
assert(results.some(row => row.case === 'resize' && row.captures.some(c => c.name === 'resized-32')), 'Missing actual resize recording');
writeFileSync(resolve(dirname(receiptFile), 'terminal-emulator-receipt.json'), JSON.stringify({emulator: '@xterm/headless 6.0.0 (xterm.js terminal core with reflow)', cases: results.map(row => ({case: row.case, captures: row.captures.length}))}, null, 2)+'\n');
console.log(`PASS xterm.js screen/cursor/reflow assertions for ${results.length} actual PTY cases`);
