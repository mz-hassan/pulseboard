const baseURL = process.env.BASE_URL || 'http://localhost:8080';
const wsURL = (process.env.WS_URL || baseURL.replace(/^http/, 'ws'));

const roomResponse = await fetch(`${baseURL}/api/rooms`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ roomId: `smoke-${Date.now()}` }),
});
if (!roomResponse.ok) throw new Error(`create room: HTTP ${roomResponse.status}`);
const room = await roomResponse.json();
const params = new URLSearchParams({ token: room.token, userId: 'smoke_user', name: 'Smoke Test', color: '#317b72' });

const first = new WebSocket(`${wsURL}/ws/${room.roomId}?${params}`);
const initialState = message(first, value => value.type === 'board:state');
await opened(first);
await initialState;
const strokeId = `stroke_${Date.now()}`;
first.send(JSON.stringify({ type: 'stroke:start', opId: 'smoke_start', strokeId, color: '#317b72', width: 4, points: [{ x: 10, y: 10 }] }));
first.send(JSON.stringify({ type: 'stroke:points', opId: 'smoke_points', strokeId, points: [{ x: 20, y: 25 }] }));
first.send(JSON.stringify({ type: 'stroke:end', opId: 'smoke_end', strokeId }));
await message(first, value => value.type === 'stroke:end' && value.strokeId === strokeId);
first.send(JSON.stringify({ type: 'latency', latencyMs: 5 }));
first.close();
await closed(first);

await fetch(`${baseURL}/api/metrics/reconnect-attempt`, { method: 'POST' });
params.set('reconnect', '1');
const second = new WebSocket(`${wsURL}/ws/${room.roomId}?${params}`);
const restoredState = message(second, value => value.type === 'board:state');
await opened(second);
const restored = await restoredState;
if (restored.strokes?.length !== 1 || restored.strokes[0].points.length !== 2) throw new Error('persisted stroke was not restored');

const metrics = await (await fetch(`${baseURL}/metrics`)).text();
for (const name of ['whiteboard_websocket_connections_peak', 'whiteboard_operations_total', 'whiteboard_stroke_round_trip_seconds_count', 'whiteboard_reconnect_successes_total']) {
  if (!metrics.includes(name)) throw new Error(`missing metric: ${name}`);
}
second.close();
await fetch(`${baseURL}/api/rooms/${room.roomId}/clear`, { method: 'POST', headers: { authorization: `Bearer ${room.token}` } });
console.log(`Smoke test passed: room creation, WebSocket sync, persistence, reconnection, and metrics (${room.roomId})`);

function opened(socket) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('WebSocket open timeout')), 5000);
    socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
    socket.addEventListener('error', () => { clearTimeout(timer); reject(new Error('WebSocket failed')); }, { once: true });
  });
}
function message(socket, predicate) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { socket.removeEventListener('message', handler); reject(new Error('message timeout')); }, 5000);
    const handler = event => { const value = JSON.parse(event.data); if (predicate(value)) { clearTimeout(timer); socket.removeEventListener('message', handler); resolve(value); } };
    socket.addEventListener('message', handler);
  });
}
function closed(socket) {
  if (socket.readyState === WebSocket.CLOSED) return Promise.resolve();
  return new Promise(resolve => socket.addEventListener('close', resolve, { once: true }));
}
