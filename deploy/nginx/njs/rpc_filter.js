// JSON-RPC filter for the public C-Chain RPC (rpc.l1beat.io). Used by rpc.conf via js_content.
// Place at /etc/nginx/njs/rpc_filter.js.
//
// Allows only eth_/net_/web3_ methods (minus account and filter/subscription methods),
// caps batch size and eth_getLogs block ranges, then hands the request to the @node
// location, which proxies it to the local archive node unchanged.
//
// Plain var and promise chains (no const/let/async) so it parses on any njs >= 0.5.

var ALLOW = /^(eth_|net_|web3_)/;
var DENY = [
  // account methods: meaningless on a shared node
  'eth_sign', 'eth_signTransaction', 'eth_sendTransaction', 'eth_accounts',
  'eth_signTypedData', 'eth_signTypedData_v4',
  // per-caller state on the node
  'eth_newFilter', 'eth_newBlockFilter', 'eth_newPendingTransactionFilter',
  'eth_getFilterChanges', 'eth_getFilterLogs', 'eth_uninstallFilter',
  'eth_subscribe', 'eth_unsubscribe'
];
var MAX_BATCH = 10;              // limit_req counts HTTP requests: 20 r/s x 10 = 200 calls/s per IP
var LOG_RANGE_WITH_ADDRESS = 2048;
var LOG_RANGE_NO_ADDRESS = 256;  // unfiltered C-Chain logs get large fast
var RECENT_TAGS = ['latest', 'pending', 'safe', 'finalized'];
var NODE = 'http://127.0.0.1:9650/ext/bc/C/rpc';
var NEED_HEAD = 'NEED_HEAD';

// The node decodes requests with Go's encoding/json, which matches keys
// case-insensitively (and folds Unicode lookalikes such as U+212A for k). A key
// like "Method" or "FromBlock" is ignored here but read by the node, so it
// could carry a debug_ method or an unbounded log range past these checks.
// Accept only the exact field names; nothing else reaches the node.
var CALL_KEYS = ['jsonrpc', 'id', 'method', 'params'];
var FILTER_KEYS = ['fromBlock', 'toBlock', 'address', 'topics', 'blockHash'];

// The first key of obj that is not in allowed, or '' when all are.
function unexpectedKey(obj, allowed) {
  var keys = Object.keys(obj);
  for (var i = 0; i < keys.length; i++) {
    if (allowed.indexOf(keys[i]) === -1) return keys[i];
  }
  return '';
}

function cors(r) {
  r.headersOut['Access-Control-Allow-Origin'] = '*';
}

function send(r, status, obj) {
  cors(r);
  r.headersOut['Content-Type'] = 'application/json';
  r.return(status, JSON.stringify(obj));
}

function rpcError(id, code, message) {
  return { jsonrpc: '2.0', id: id === undefined ? null : id, error: { code: code, message: message } };
}

function isHex(v) {
  return typeof v === 'string' && /^0x[0-9a-fA-F]+$/.test(v);
}

function isRecent(v) {
  return v === undefined || v === null || RECENT_TAGS.indexOf(v) !== -1;
}

function hasAddress(f) {
  if (typeof f.address === 'string') return f.address.length > 0;
  return Array.isArray(f.address) && f.address.length > 0;
}

// '' when the eth_getLogs filter is fine, NEED_HEAD when it can only be judged
// against the current block, otherwise an error message. head is a number or null.
function checkLogs(params, head) {
  var f = Array.isArray(params) ? params[0] : null;
  if (!f || typeof f !== 'object' || Array.isArray(f)) return 'eth_getLogs needs a filter object';
  var extra = unexpectedKey(f, FILTER_KEYS);
  if (extra) return 'eth_getLogs filter has an unsupported field: ' + extra;
  if (f.blockHash) return '';
  var withAddr = hasAddress(f);
  var max = withAddr ? LOG_RANGE_WITH_ADDRESS : LOG_RANGE_NO_ADDRESS;
  var from = f.fromBlock, to = f.toBlock;
  if (from === 'earliest' || to === 'earliest') return 'eth_getLogs from earliest is not allowed';
  if (isRecent(from) && isRecent(to)) return '';
  if (!isHex(from)) return 'eth_getLogs needs a hex fromBlock, or blockHash';
  var a = parseInt(from, 16);
  if (a === 0) return 'eth_getLogs from block 0 is not allowed';
  var b;
  if (isHex(to)) b = parseInt(to, 16);
  else if (isRecent(to)) {
    if (head === null) return NEED_HEAD;
    b = head;
  } else return 'eth_getLogs has an invalid toBlock';
  if (b - a > max) {
    return 'eth_getLogs range is limited to ' + max + ' blocks' +
      (withAddr ? '' : ' without an address filter, ' + LOG_RANGE_WITH_ADDRESS + ' with one');
  }
  return '';
}

// Runs every eth_getLogs check against head. Returns [call, message] for the first failure, or null.
function firstLogError(calls, head) {
  for (var i = 0; i < calls.length; i++) {
    if (calls[i].method !== 'eth_getLogs') continue;
    var why = checkLogs(calls[i].params, head);
    if (why) return [calls[i], why];
  }
  return null;
}

function getHead() {
  return ngx.fetch(NODE, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}'
  })
    .then(function (res) { return res.json(); })
    .then(function (j) {
      var h = parseInt(j.result, 16);
      if (isNaN(h)) throw new Error('bad head');
      return h;
    });
}

function forward(r, calls, head) {
  var bad = firstLogError(calls, head);
  if (bad) {
    send(r, 400, rpcError(bad[0].id, -32602, bad[1]));
    return;
  }
  // The body has already been read, and the proxy forwards it unchanged.
  r.internalRedirect('@node');
}

function filter(r) {
  if (r.method === 'OPTIONS') {
    cors(r);
    r.headersOut['Access-Control-Allow-Methods'] = 'POST, OPTIONS';
    r.headersOut['Access-Control-Allow-Headers'] = 'Content-Type, Authorization';
    r.headersOut['Access-Control-Max-Age'] = '86400';
    r.return(204);
    return;
  }
  if (r.method !== 'POST') {
    send(r, 405, rpcError(null, -32600, 'Use POST'));
    return;
  }
  var text = r.requestText;
  if (!text) {
    send(r, 400, rpcError(null, -32700, 'Empty or oversized body'));
    return;
  }
  var body;
  try {
    body = JSON.parse(text);
  } catch (e) {
    send(r, 400, rpcError(null, -32700, 'Parse error'));
    return;
  }
  var calls = Array.isArray(body) ? body : [body];
  if (calls.length === 0 || calls.length > MAX_BATCH) {
    send(r, 400, rpcError(null, -32600, 'Batch limit is ' + MAX_BATCH));
    return;
  }
  var needHead = false;
  for (var i = 0; i < calls.length; i++) {
    var c = calls[i];
    if (!c || typeof c !== 'object' || Array.isArray(c)) {
      send(r, 400, rpcError(null, -32600, 'Invalid request'));
      return;
    }
    var extra = unexpectedKey(c, CALL_KEYS);
    if (extra) {
      send(r, 400, rpcError(c.id, -32600, 'Invalid request: unsupported field ' + extra));
      return;
    }
    var m = typeof c.method === 'string' ? c.method : '';
    if (!ALLOW.test(m) || DENY.indexOf(m) !== -1) {
      send(r, 403, rpcError(c && c.id, -32601, 'Method not available on the public endpoint: ' + m));
      return;
    }
    if (m === 'eth_getLogs' && checkLogs(c.params, null) === NEED_HEAD) needHead = true;
  }
  if (!needHead) {
    forward(r, calls, null);
    return;
  }
  // njs starts a fresh VM per request, so there's no cache: one cheap localhost call, only for this shape.
  getHead()
    .then(function (head) { forward(r, calls, head); })
    .catch(function () { send(r, 503, rpcError(null, -32603, 'Could not read the current block, try again')); });
}

export default { filter };
