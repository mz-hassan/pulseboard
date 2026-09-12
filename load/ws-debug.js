import http from 'k6/http';
import ws from 'k6/ws';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL;
const WS_URL = __ENV.WS_URL;

export const options = { vus: 1, iterations: 1 };

export function setup() {
  const room = http.post(`${BASE_URL}/api/rooms`, JSON.stringify({ roomId: `debug-${Date.now()}` }), { headers: { 'Content-Type': 'application/json' } });
  check(room, { 'room created': r => r.status === 201 });
  return room.json();
}

export default function (room) {
  const url = `${WS_URL}/ws/${room.roomId}?token=${encodeURIComponent(room.token)}&userId=debug_1&name=Debug&color=%23317b72`;
  const response = ws.connect(url, {}, socket => {
    socket.on('open', () => {
      console.log('WS_OPEN');
      socket.setTimeout(() => socket.close(), 2000);
    });
    socket.on('message', msg => console.log(`WS_MESSAGE ${String(msg).slice(0, 120)}`));
    socket.on('error', err => console.log(`WS_ERROR ${JSON.stringify(err)}`));
    socket.on('close', () => console.log('WS_CLOSE'));
  });
  console.log(`WS_RESPONSE status=${response && response.status} error=${response && response.error}`);
  check(response, { 'websocket upgraded': r => r && r.status === 101 });
}
