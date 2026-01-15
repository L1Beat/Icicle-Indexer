import { useState } from 'react';
import { Play, Copy, Check } from 'lucide-react';

interface Endpoint {
  name: string;
  method: string;
  path: string;
  params: { name: string; type: 'path' | 'query'; default?: string; options?: string[] }[];
}

const endpoints: Endpoint[] = [
  // Health & Status
  { name: 'Health Check', method: 'GET', path: '/health', params: [] },
  { name: 'Indexer Status', method: 'GET', path: '/api/v1/metrics/indexer/status', params: [] },

  // EVM Data
  {
    name: 'List Blocks',
    method: 'GET',
    path: '/api/v1/data/evm/{chainId}/blocks',
    params: [
      { name: 'chainId', type: 'path', default: '43114' },
      { name: 'limit', type: 'query', default: '10' },
      { name: 'offset', type: 'query', default: '0' },
    ],
  },
  {
    name: 'Get Block',
    method: 'GET',
    path: '/api/v1/data/evm/{chainId}/blocks/{number}',
    params: [
      { name: 'chainId', type: 'path', default: '43114' },
      { name: 'number', type: 'path', default: '75000000' },
    ],
  },
  {
    name: 'List Transactions',
    method: 'GET',
    path: '/api/v1/data/evm/{chainId}/txs',
    params: [
      { name: 'chainId', type: 'path', default: '43114' },
      { name: 'limit', type: 'query', default: '10' },
    ],
  },
  {
    name: 'Address Transactions',
    method: 'GET',
    path: '/api/v1/data/evm/{chainId}/address/{address}/txs',
    params: [
      { name: 'chainId', type: 'path', default: '43114' },
      { name: 'address', type: 'path', default: '0x' },
      { name: 'limit', type: 'query', default: '10' },
    ],
  },

  // EVM Metrics
  {
    name: 'Chain Stats',
    method: 'GET',
    path: '/api/v1/metrics/evm/{chainId}/stats',
    params: [{ name: 'chainId', type: 'path', default: '43114' }],
  },
  {
    name: 'List Metrics',
    method: 'GET',
    path: '/api/v1/metrics/evm/{chainId}/timeseries',
    params: [{ name: 'chainId', type: 'path', default: '43114' }],
  },
  {
    name: 'Get Metric Data',
    method: 'GET',
    path: '/api/v1/metrics/evm/{chainId}/timeseries/{metric}',
    params: [
      { name: 'chainId', type: 'path', default: '43114' },
      { name: 'metric', type: 'path', default: 'tx_count', options: [
        'tx_count', 'active_addresses', 'active_senders', 'fees_paid', 'gas_used',
        'contracts', 'deployers', 'avg_tps', 'max_tps', 'avg_gps', 'max_gps',
        'avg_gas_price', 'max_gas_price', 'icm_total', 'icm_sent', 'icm_received',
        'usdc_volume', 'cumulative_tx_count', 'cumulative_addresses', 'cumulative_contracts', 'cumulative_deployers'
      ]},
      { name: 'granularity', type: 'query', default: 'day', options: ['hour', 'day', 'week', 'month'] },
      { name: 'limit', type: 'query', default: '30' },
      { name: 'from', type: 'query', default: '' },
      { name: 'to', type: 'query', default: '' },
    ],
  },

  // P-Chain
  {
    name: 'P-Chain Transactions',
    method: 'GET',
    path: '/api/v1/data/pchain/txs',
    params: [
      { name: 'limit', type: 'query', default: '10' },
      { name: 'tx_type', type: 'query', default: '' },
    ],
  },
  {
    name: 'P-Chain Tx Types',
    method: 'GET',
    path: '/api/v1/data/pchain/tx-types',
    params: [],
  },

  // Subnets
  {
    name: 'List Subnets',
    method: 'GET',
    path: '/api/v1/data/subnets',
    params: [
      { name: 'type', type: 'query', default: '', options: ['', 'regular', 'elastic', 'l1'] },
      { name: 'limit', type: 'query', default: '20' },
    ],
  },
  {
    name: 'Get Subnet',
    method: 'GET',
    path: '/api/v1/data/subnets/{subnetId}',
    params: [{ name: 'subnetId', type: 'path', default: '' }],
  },
  {
    name: 'List L1s',
    method: 'GET',
    path: '/api/v1/data/l1s',
    params: [{ name: 'limit', type: 'query', default: '20' }],
  },
  {
    name: 'List Chains',
    method: 'GET',
    path: '/api/v1/data/chains',
    params: [{ name: 'limit', type: 'query', default: '20' }],
  },

  // Validators
  {
    name: 'List Validators',
    method: 'GET',
    path: '/api/v1/data/validators',
    params: [
      { name: 'subnet_id', type: 'query', default: '' },
      { name: 'active', type: 'query', default: '', options: ['', 'true', 'false'] },
      { name: 'limit', type: 'query', default: '20' },
    ],
  },
  {
    name: 'Get Validator',
    method: 'GET',
    path: '/api/v1/data/validators/{id}',
    params: [{ name: 'id', type: 'path', default: '' }],
  },
  {
    name: 'Validator Deposits',
    method: 'GET',
    path: '/api/v1/data/validators/{id}/deposits',
    params: [{ name: 'id', type: 'path', default: '' }],
  },

  // Metrics
  {
    name: 'L1 Fee Stats',
    method: 'GET',
    path: '/api/v1/metrics/fees',
    params: [
      { name: 'subnet_id', type: 'query', default: '' },
      { name: 'limit', type: 'query', default: '20' },
    ],
  },
];

function ApiPlayground() {
  const [baseUrl, setBaseUrl] = useState('http://localhost:8080');
  const [selectedEndpoint, setSelectedEndpoint] = useState<Endpoint>(endpoints[0]);
  const [paramValues, setParamValues] = useState<Record<string, string>>({});
  const [response, setResponse] = useState<string>('');
  const [loading, setLoading] = useState(false);
  const [responseTime, setResponseTime] = useState<number | null>(null);
  const [copied, setCopied] = useState(false);

  const handleEndpointChange = (name: string) => {
    const endpoint = endpoints.find((e) => e.name === name);
    if (endpoint) {
      setSelectedEndpoint(endpoint);
      // Reset params to defaults
      const defaults: Record<string, string> = {};
      endpoint.params.forEach((p) => {
        defaults[p.name] = p.default || '';
      });
      setParamValues(defaults);
      setResponse('');
      setResponseTime(null);
    }
  };

  const buildUrl = () => {
    let path = selectedEndpoint.path;
    const queryParams: string[] = [];

    selectedEndpoint.params.forEach((param) => {
      const value = paramValues[param.name] || param.default || '';
      if (param.type === 'path') {
        path = path.replace(`{${param.name}}`, value);
      } else if (param.type === 'query' && value) {
        queryParams.push(`${param.name}=${encodeURIComponent(value)}`);
      }
    });

    const queryString = queryParams.length > 0 ? `?${queryParams.join('&')}` : '';
    return `${baseUrl}${path}${queryString}`;
  };

  const executeRequest = async () => {
    setLoading(true);
    const url = buildUrl();
    const startTime = performance.now();

    try {
      const res = await fetch(url);
      const data = await res.json();
      setResponse(JSON.stringify(data, null, 2));
      setResponseTime(Math.round(performance.now() - startTime));
    } catch (error) {
      setResponse(JSON.stringify({ error: String(error) }, null, 2));
      setResponseTime(null);
    } finally {
      setLoading(false);
    }
  };

  const copyUrl = () => {
    navigator.clipboard.writeText(buildUrl());
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold text-gray-900">API Playground</h1>
        <div className="flex items-center gap-2">
          <select
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            className="px-3 py-2 border border-gray-300 rounded-lg text-sm"
          >
            <option value="http://localhost:8080">localhost:8080</option>
            <option value="http://135.181.116.124:8080">Production (135.181.116.124)</option>
          </select>
          <input
            type="text"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            className="px-3 py-2 border border-gray-300 rounded-lg text-sm w-64"
            placeholder="Or enter custom URL"
          />
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Request Builder */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6 space-y-4">
          <h2 className="text-lg font-semibold text-gray-800">Request</h2>

          {/* Endpoint Selector */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">Endpoint</label>
            <select
              value={selectedEndpoint.name}
              onChange={(e) => handleEndpointChange(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            >
              {endpoints.map((e) => (
                <option key={e.name} value={e.name}>
                  {e.method} {e.path} - {e.name}
                </option>
              ))}
            </select>
          </div>

          {/* Parameters */}
          {selectedEndpoint.params.length > 0 && (
            <div className="space-y-3">
              <label className="block text-sm font-medium text-gray-700">Parameters</label>
              {selectedEndpoint.params.map((param) => (
                <div key={param.name} className="flex items-center gap-2">
                  <span className="text-sm text-gray-600 w-28 flex-shrink-0">
                    {param.name}
                    <span className="text-xs text-gray-400 ml-1">({param.type})</span>
                  </span>
                  {param.options ? (
                    <select
                      value={paramValues[param.name] || param.default || ''}
                      onChange={(e) =>
                        setParamValues({ ...paramValues, [param.name]: e.target.value })
                      }
                      className="flex-1 px-3 py-1.5 border border-gray-300 rounded-lg text-sm"
                    >
                      {param.options.map((opt) => (
                        <option key={opt} value={opt}>
                          {opt || '(empty)'}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      type="text"
                      value={paramValues[param.name] || param.default || ''}
                      onChange={(e) =>
                        setParamValues({ ...paramValues, [param.name]: e.target.value })
                      }
                      className="flex-1 px-3 py-1.5 border border-gray-300 rounded-lg text-sm"
                      placeholder={param.default}
                    />
                  )}
                </div>
              ))}
            </div>
          )}

          {/* URL Preview */}
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">URL</label>
            <div className="flex items-center gap-2">
              <code className="flex-1 px-3 py-2 bg-gray-100 rounded-lg text-sm text-gray-800 overflow-x-auto">
                {buildUrl()}
              </code>
              <button
                onClick={copyUrl}
                className="p-2 text-gray-500 hover:text-gray-700 hover:bg-gray-100 rounded-lg"
              >
                {copied ? <Check size={18} className="text-green-500" /> : <Copy size={18} />}
              </button>
            </div>
          </div>

          {/* Execute Button */}
          <button
            onClick={executeRequest}
            disabled={loading}
            className="w-full flex items-center justify-center gap-2 px-4 py-2.5 bg-gray-900 text-white rounded-lg font-medium hover:bg-gray-800 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Play size={18} />
            {loading ? 'Loading...' : 'Execute'}
          </button>
        </div>

        {/* Response */}
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-6 space-y-4">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-gray-800">Response</h2>
            {responseTime !== null && (
              <span className="text-sm text-gray-500">{responseTime}ms</span>
            )}
          </div>
          <pre className="bg-gray-900 text-gray-100 p-4 rounded-lg overflow-auto max-h-[600px] text-sm">
            {response || 'Click Execute to see response'}
          </pre>
        </div>
      </div>
    </div>
  );
}

export default ApiPlayground;
