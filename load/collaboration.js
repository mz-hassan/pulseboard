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
const CONNECTION_LIFETIME = __ENV.CONNECTION_LIFETIME || '45s';
const RUN_ID = __ENV.RUN_ID || `run-${Date.now()}`;
const RECONNECT_MODE = (__ENV.RECONNECT_MODE || 'false') === 'true';

const strokeLatency = new Trend('stroke_round_trip_ms', true);
const operations = new Counter('drawing_operations');
const reconnectSuccess = new Rate('reconnection_success');
const socketSuccess = new Rate('websocket_connection_success');
const connectionLifetime = new Trend('websocket_connection_lifetime_ms', true);

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
    const response = http.post(`${BASE_URL}/api/rooms`, JSON.stringify({ roomId: `${RUN_ID}-${i}`.replace(/[^a-zA-Z0-9_-]/g, '-') }), { headers: { 'Content-Type': 'application/json' } });
    check(response, { 'room created': r => r.status === 201 });
    rooms.push(response.json());
  }
  return { rooms };
}

let connectedBefore = false;
export default function (data) {
  const room = data.rooms[(__VU - 1) % data.rooms.length];
  const reconnecting = RECONNECT_MODE && connectedBefore;
  if (reconnecting) http.post(`${BASE_URL}/api/metrics/reconnect-attempt`);
  const query = `token=${encodeURIComponent(room.token)}&userId=${encodeURIComponent(`vu_${__VU}`)}&name=${encodeURIComponent(`Load ${__VU}`)}&color=%23317b72${reconnecting ? '&reconnect=1' : ''}`;
  const pending = new Map();
  const openedAt = Date.now();
  const random = prng((__VU * 1000003) + (__ITER * 9176));
  const response = ws.connect(`${WS_URL}/ws/${room.roomId}?${query}`, {}, socket => {
    socket.on('open', () => {
      socketSuccess.add(true);
      if (reconnecting) reconnectSuccess.add(true);
      connectedBefore = true;
      let sequence = 0;
      socket.setInterval(() => {
        const strokeId = `s_${__VU}_${__ITER}_${sequence++}`;
        const { start, points, color, width } = realisticStroke(random);
        send(socket, pending, 'stroke:start', strokeId, [start], color, width);
        send(socket, pending, 'stroke:points', strokeId, points, color, width);
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
    socket.on('close', () => connectionLifetime.add(Date.now() - openedAt));
    socket.setTimeout(() => socket.close(), durationMs(CONNECTION_LIFETIME));
  });
  check(response, { 'websocket upgraded': r => r && r.status === 101 });
}

function durationMs(value) {
  const match = String(value).match(/^(\d+(?:\.\d+)?)(ms|s|m)$/);
  if (!match) return 45000;
  const factor = match[2] === 'm' ? 60000 : match[2] === 's' ? 1000 : 1;
  return Number(match[1]) * factor;
}

function prng(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 4294967296;
  };
}

function realisticStroke(random) {
  const x = 30 + random() * 1100;
  const y = 30 + random() * 650;
  const angle = random() * Math.PI * 2;
  const length = 15 + random() * 180;
  const count = 3 + Math.floor(random() * 8);
  const points = [];
  for (let i = 1; i <= count; i++) {
    const progress = i / count;
    const wobble = (random() - 0.5) * 12;
    points.push({ x: x + Math.cos(angle) * length * progress + wobble, y: y + Math.sin(angle) * length * progress + wobble });
  }
  const colors = ['#317b72', '#2563eb', '#dc2626', '#7c3aed', '#ea580c'];
  return { start: { x, y }, points, color: colors[Math.floor(random() * colors.length)], width: 2 + Math.floor(random() * 6) };
}

function send(socket, pending, type, strokeId, points, color, width) {
  const opId = `${__VU}-${__ITER}-${Date.now()}-${Math.random()}`;
  pending.set(opId, Date.now());
  socket.send(JSON.stringify({ type, opId, strokeId, points, color, width }));
  operations.add(1);
}
