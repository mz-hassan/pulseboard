(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const canvas = $('board'), ctx = canvas.getContext('2d');
  const state = { room:'', token:'', userId:sessionStorage.getItem('pulse-user') || crypto.randomUUID().replaceAll('-','_'), name:'', color:'#20221f', width:4, mode:'pen', ws:null, connected:false, reconnects:0, reconnectTimer:null, drawing:null, strokes:[], remote:new Map(), peers:new Map(), pending:new Map(), cursorSent:0 };
  sessionStorage.setItem('pulse-user', state.userId);

  function resize(){ const rect=canvas.getBoundingClientRect(),dpr=Math.min(devicePixelRatio||1,2);canvas.width=Math.round(rect.width*dpr);canvas.height=Math.round(rect.height*dpr);ctx.setTransform(dpr,0,0,dpr,0,0);redraw(); }
  function line(points,color,width){ if(!points?.length)return;ctx.strokeStyle=color;ctx.lineWidth=width;ctx.lineCap='round';ctx.lineJoin='round';ctx.beginPath();ctx.moveTo(points[0].x,points[0].y);if(points.length===1)ctx.lineTo(points[0].x+.01,points[0].y+.01);for(let i=1;i<points.length;i++)ctx.lineTo(points[i].x,points[i].y);ctx.stroke(); }
  function redraw(){ctx.clearRect(0,0,canvas.clientWidth,canvas.clientHeight);state.strokes.forEach(s=>line(s.points,s.color,s.width));state.remote.forEach(s=>line(s.points,s.color,s.width));if(state.drawing)line(state.drawing.points,state.drawing.color,state.drawing.width)}
  function point(e){const r=canvas.getBoundingClientRect();return{x:Math.max(0,Math.min(r.width,e.clientX-r.left)),y:Math.max(0,Math.min(r.height,e.clientY-r.top))}}
  function op(type,data={}){if(!state.connected)return;const opId=crypto.randomUUID();state.pending.set(opId,performance.now());state.ws.send(JSON.stringify({type,opId,...data}))}
  canvas.addEventListener('pointerdown',e=>{if(!state.connected)return;canvas.setPointerCapture(e.pointerId);const p=point(e),color=state.mode==='eraser'?'#f8f6ef':state.color,width=state.mode==='eraser'?22:state.width;state.drawing={id:crypto.randomUUID(),color,width,points:[p]};line([p],color,width);op('stroke:start',{strokeId:state.drawing.id,color,width,points:[p]})});
  canvas.addEventListener('pointermove',e=>{const p=point(e);if(state.drawing){const prev=state.drawing.points.at(-1);state.drawing.points.push(p);line([prev,p],state.drawing.color,state.drawing.width);op('stroke:points',{strokeId:state.drawing.id,points:[p]})}if(state.connected&&performance.now()-state.cursorSent>33){state.cursorSent=performance.now();state.ws.send(JSON.stringify({type:'cursor',x:p.x,y:p.y}))}});
  function endDraw(){if(!state.drawing)return;op('stroke:end',{strokeId:state.drawing.id});state.strokes.push(state.drawing);state.drawing=null}
  canvas.addEventListener('pointerup',endDraw);canvas.addEventListener('pointercancel',endDraw);

  function connect(isReconnect=false){
    clearTimeout(state.reconnectTimer);setConnection('connecting','Connecting');
    const protocol=location.protocol==='https:'?'wss':'ws',q=new URLSearchParams({token:state.token,userId:state.userId,name:state.name,color:avatarColor(state.userId)});if(isReconnect)q.set('reconnect','1');
    const ws=new WebSocket(`${protocol}://${location.host}/ws/${encodeURIComponent(state.room)}?${q}`);state.ws=ws;
    ws.onopen=()=>{state.connected=true;state.reconnects=0;setConnection('online','Live');$('gate').classList.add('hidden')};
    ws.onmessage=e=>{let msg;try{msg=JSON.parse(e.data)}catch{return}handle(msg)};
    ws.onclose=()=>{state.connected=false;setConnection('offline','Offline');if(state.room){const wait=Math.min(1000*2**state.reconnects++,10000);fetch('/api/metrics/reconnect-attempt',{method:'POST'}).catch(()=>{});state.reconnectTimer=setTimeout(()=>connect(true),wait)}};
    ws.onerror=()=>ws.close();
  }
  function handle(msg){
    if(msg.opId&&msg.userId===state.userId){const began=state.pending.get(msg.opId);if(began!==undefined){state.pending.delete(msg.opId);state.ws.send(JSON.stringify({type:'latency',latencyMs:performance.now()-began}))}return}
    if(msg.type==='board:state'){state.strokes=msg.strokes||[];redraw()}
    else if(msg.type==='board:clear'){state.strokes=[];state.remote.clear();redraw();toast('Board cleared')}
    else if(msg.type==='stroke:start'){state.remote.set(msg.strokeId,{id:msg.strokeId,userId:msg.userId,color:msg.color,width:msg.width,points:msg.points||[]});line(msg.points,msg.color,msg.width)}
    else if(msg.type==='stroke:points'){const s=state.remote.get(msg.strokeId);if(s){const before=s.points.at(-1);s.points.push(...msg.points);line([before,...msg.points],s.color,s.width)}}
    else if(msg.type==='stroke:end'){const s=state.remote.get(msg.strokeId);if(s){state.remote.delete(msg.strokeId);state.strokes.push(s)}}
    else if(msg.type==='presence:snapshot'){state.peers.clear();(msg.peers||[]).forEach(addPeer);renderPresence()}
    else if(msg.type==='presence:join'){addPeer(msg.peer);renderPresence()}
    else if(msg.type==='presence:leave'){state.peers.delete(msg.userId);$('cursor-'+safe(msg.userId))?.remove();renderPresence()}
    else if(msg.type==='cursor'&&msg.userId!==state.userId){renderCursor(msg)}
  }
  function addPeer(peer){if(peer&&peer.id!==state.userId)state.peers.set(peer.id,peer)}
  function renderPresence(){const all=[{id:state.userId,name:state.name,color:avatarColor(state.userId)},...state.peers.values()];$('presence').innerHTML=all.slice(0,5).map(p=>`<span class="avatar" title="${escapeHTML(p.name)}" style="background:${p.color}">${escapeHTML(initials(p.name))}</span>`).join('')}
  function renderCursor(msg){const peer=state.peers.get(msg.userId);if(!peer)return;let el=$('cursor-'+safe(msg.userId));if(!el){el=document.createElement('span');el.id='cursor-'+safe(msg.userId);el.className='remote-cursor';el.style.color=peer.color;el.innerHTML=`<i></i><b>${escapeHTML(peer.name)}</b>`;$('cursors').append(el)}el.style.transform=`translate(${msg.x}px,${msg.y}px)`}

  let joinMode=false;
  function parseInvite(){const p=new URLSearchParams(location.hash.slice(1));if(p.get('room')&&p.get('token')){joinMode=true;$('room-input').value=p.get('room');$('token-input').value=p.get('token');updateGate()}}
  function updateGate(){$('room-field').classList.toggle('hidden',!joinMode);$('token-field').classList.toggle('hidden',!joinMode);$('primary-action').textContent=joinMode?'Join room':'Create a room';$('toggle-mode').textContent=joinMode?'Create a new room':'Join an existing room'}
  $('toggle-mode').onclick=()=>{joinMode=!joinMode;updateGate()};
  $('primary-action').onclick=async()=>{const name=$('display-name').value.trim();if(!name){showError('Add your display name first.');return}$('primary-action').disabled=true;try{state.name=name;localStorage.setItem('pulse-name',name);if(joinMode){state.room=$('room-input').value.trim();state.token=$('token-input').value.trim();if(!state.room||!state.token)throw Error('Room ID and access token are required.')}else{const res=await fetch('/api/rooms',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'}),data=await res.json();if(!res.ok)throw Error(data.error||'Could not create room.');state.room=data.roomId;state.token=data.token;location.hash=new URLSearchParams({room:state.room,token:state.token})}saveRoom();$('room-label').textContent=state.room;renderPresence();connect()}catch(e){showError(e.message)}finally{$('primary-action').disabled=false}};
  $('display-name').value=localStorage.getItem('pulse-name')||'';
  ['display-name','room-input','token-input'].forEach(id=>$(id).addEventListener('keydown',e=>{if(e.key==='Enter')$('primary-action').click()}));
  function saveRoom(){sessionStorage.setItem('pulse-room',JSON.stringify({room:state.room,token:state.token,name:state.name}))}
  function resume(){const hash=new URLSearchParams(location.hash.slice(1));if(hash.get('room')&&hash.get('token'))return false;try{const saved=JSON.parse(sessionStorage.getItem('pulse-room'));if(saved?.room&&saved?.token&&saved?.name){Object.assign(state,saved);$('room-label').textContent=state.room;renderPresence();connect();return true}}catch{}return false}

  $('pen').onclick=()=>setTool('pen');$('eraser').onclick=()=>setTool('eraser');function setTool(tool){state.mode=tool;$('pen').classList.toggle('active',tool==='pen');$('eraser').classList.toggle('active',tool==='eraser')}
  $('color').oninput=e=>{state.color=e.target.value;document.querySelector('.color-wrap span').style.background=state.color};
  $('width').oninput=e=>{state.width=Number(e.target.value);const size=Math.max(5,state.width);$('width-preview').style.width=size+'px';$('width-preview').style.height=size+'px'};
  $('invite').onclick=async()=>{const url=`${location.origin}${location.pathname}#${new URLSearchParams({room:state.room,token:state.token})}`;await navigator.clipboard.writeText(url);toast('Invite link copied')};
  $('clear').onclick=async()=>{if(!confirm('Clear the board for everyone?'))return;const res=await fetch(`/api/rooms/${encodeURIComponent(state.room)}/clear`,{method:'POST',headers:{Authorization:`Bearer ${state.token}`}});if(!res.ok)toast('Could not clear board')};
  addEventListener('keydown',e=>{if(/input/i.test(e.target.tagName))return;if(e.key.toLowerCase()==='p')setTool('pen');if(e.key.toLowerCase()==='e')setTool('eraser')});
  function setConnection(kind,label){$('connection').className='connection '+kind;$('connection').querySelector('b').textContent=label}
  function showError(message){$('gate-error').textContent=message}function toast(message){$('toast').textContent=message;$('toast').classList.add('show');setTimeout(()=>$('toast').classList.remove('show'),1800)}
  function avatarColor(value){const colors=['#e05d48','#317b72','#6d5cc7','#b36b20','#296caa','#9a3f75'];let n=0;for(const c of value)n=(n*31+c.charCodeAt(0))|0;return colors[Math.abs(n)%colors.length]}
  function initials(name){return name.split(/\s+/).slice(0,2).map(x=>x[0]||'').join('').toUpperCase()}function safe(v){return v.replace(/[^\w-]/g,'')}function escapeHTML(v=''){return v.replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]))}
  addEventListener('resize',resize);resize();if(!resume())parseInvite();
})();
