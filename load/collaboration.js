import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const WS_URL = __ENV.WS_URL || BASE_URL.replace(/^http/, 'ws');
const VUS = Number(__ENV.VUS || 100);
const DURATION = __ENV.TEST_DURATION || '30s';
const OPS = Math.max(1, Number(__ENV.OPS_PER_SECOND || 10));
const ROOM_COUNT = Math.max(1, Number(__ENV.ROOMS || 1));

const strokeLatency = new Trend('stroke_round_trip_ms', true);
const operations = new Counter('drawing_operations');
const reconnectSuccess = new Rate('reconnection_success');
const socketSuccess = new Rate('websocket_connection_success');

export const options = {
  scenarios: { collaboration: { executor: 'constant-vus', vus: VUS, duration: DURATION, gracefulStop: '5s' } },
  thresholds: {
    websocket_connection_success: ['rate>0.99'],
    reconnection_success: ['rate>0.95'],
    stroke_round_trip_ms: ['p(95)<150', 'p(99)<300'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const rooms = [];
  for (let i = 0; i < ROOM_COUNT; i++) {
    const response = http.post(`${BASE_URL}/api/rooms`, JSON.stringify({ roomId: `load-${Date.now()}-${i}` }), { headers: { 'Content-Type': 'application/json' } });
    check(response, { 'room created': r => r.status === 201 });
    rooms.push(response.json());
  }
  return { rooms };
}

let connectedBefore = false;
export default function (data) {
  const room = data.rooms[(__VU - 1) % data.rooms.length];
  const reconnecting = connectedBefore;
  if (reconnecting) http.post(`${BASE_URL}/api/metrics/reconnect-attempt`);
  const query = new URLSearchParams({ token: room.token, userId: `vu_${__VU}`, name: `Load ${__VU}`, color: '#317b72' });
  if (reconnecting) query.set('reconnect', '1');
  const pending = new Map();
  const response = ws.connect(`${WS_URL}/ws/${room.roomId}?${query}`, {}, socket => {
    socket.on('open', () => {
      socketSuccess.add(true);
      if (reconnecting) reconnectSuccess.add(true);
      connectedBefore = true;
      let sequence = 0;
      socket.setInterval(() => {
        const strokeId = `s_${__VU}_${__ITER}_${sequence++}`;
        const x = 20 + (sequence % 500), y = 20 + ((__VU * 13) % 400);
        send(socket, pending, 'stroke:start', strokeId, [{ x, y }], '#317b72', 4);
        send(socket, pending, 'stroke:points', strokeId, [{ x: x + 3, y: y + 2 }]);
        send(socket, pending, 'stroke:end', strokeId);
      }, 1000 / OPS);
    });
    socket.on('message', raw => {
      let msg; try { msg = JSON.parse(raw) } catch (_) { return }
      if (msg.userId === `vu_${__VU}` && pending.has(msg.opId)) {
        const latency = Date.now() - pending.get(msg.opId); pending.delete(msg.opId);
        strokeLatency.add(latency); socket.send(JSON.stringify({ type: 'latency', latencyMs: latency }));
      }
    });
    socket.on('error', () => { socketSuccess.add(false); if (reconnecting) reconnectSuccess.add(false) });
    socket.setTimeout(() => socket.close(), 10000);
  });
  check(response, { 'websocket upgraded': r => r && r.status === 101 });
}

function send(socket, pending, type, strokeId, points, color, width) {
  const opId = `${__VU}-${__ITER}-${Date.now()}-${Math.random()}`;
  pending.set(opId, Date.now());
  socket.send(JSON.stringify({ type, opId, strokeId, points, color, width }));
  operations.add(1);
}
