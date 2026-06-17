import { useState } from 'react';
import { NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom';
import {
  LayoutDashboard,
  Activity,
  Boxes,
  Network,
  BarChart3,
  Play,
  Search,
} from 'lucide-react';
import { useIndexerStatus } from '../lib/hooks';
import { timeAgo } from '../lib/format';

const DEFAULT_CHAIN = 43114;

interface NavItem {
  to: string;
  prefix: string;
  label: string;
  icon: typeof LayoutDashboard;
}

interface NavGroup {
  title: string;
  items: NavItem[];
}

const NAV: NavGroup[] = [
  {
    title: 'Operations',
    items: [
      { to: '/overview', prefix: '/overview', label: 'Overview', icon: LayoutDashboard },
      { to: '/sync-status', prefix: '/sync-status', label: 'Sync & Storage', icon: Activity },
    ],
  },
  {
    title: 'Explore',
    items: [
      { to: '/evm/43114/blocks', prefix: '/evm/', label: 'C-Chain', icon: Boxes },
      { to: '/p-chain/overview', prefix: '/p-chain', label: 'P-Chain', icon: Network },
    ],
  },
  {
    title: 'Analytics',
    items: [
      { to: '/evm-metrics/43114/7d', prefix: '/evm-metrics', label: 'Metrics', icon: BarChart3 },
      { to: '/api-playground', prefix: '/api-playground', label: 'API', icon: Play },
    ],
  },
];

function HealthBadge() {
  const { data, isLoading } = useIndexerStatus();
  if (isLoading && !data) {
    return <span className="text-sm text-gray-400">checking…</span>;
  }
  const healthy = data?.healthy ?? false;
  return (
    <div className="flex items-center gap-2 text-sm">
      <span
        className={`inline-block w-2.5 h-2.5 rounded-full ${healthy ? 'bg-green-500' : 'bg-red-500'}`}
      />
      <span className={healthy ? 'text-gray-700' : 'text-red-600 font-medium'}>
        {healthy ? 'Healthy' : 'Degraded'}
      </span>
      {data?.last_update && (
        <span className="text-gray-400">· updated {timeAgo(data.last_update)}</span>
      )}
    </div>
  );
}

function Layout() {
  const navigate = useNavigate();
  const location = useLocation();
  const { data: status } = useIndexerStatus();
  const [query, setQuery] = useState('');

  // The "current" chain comes from the URL when on an explorer page, else the
  // default. The switcher and search both target this chain.
  const chainMatch = location.pathname.match(/^\/evm\/(\d+)(?:\/|$)/);
  const currentChain = chainMatch ? parseInt(chainMatch[1], 10) : DEFAULT_CHAIN;
  const evmChains = status?.evm ?? [];

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    const q = query.trim();
    if (!q) return;
    if (/^\d+$/.test(q)) navigate(`/evm/${currentChain}/block/${q}`);
    else if (/^0x[0-9a-fA-F]{64}$/.test(q)) navigate(`/evm/${currentChain}/tx/${q}`);
    else if (/^0x[0-9a-fA-F]{40}$/.test(q)) navigate(`/evm/${currentChain}/address/${q}`);
    else navigate(`/p-chain/tx/${q}`);
    setQuery('');
  };

  return (
    <div className="min-h-screen bg-gray-50">
      {/* Sidebar */}
      <aside className="fixed inset-y-0 left-0 w-60 bg-white border-r border-gray-200 flex flex-col">
        <div className="px-6 py-5 border-b border-gray-100">
          <div className="text-lg font-bold text-gray-900">Icicle</div>
          <div className="text-xs text-gray-400">Avalanche indexer</div>
        </div>
        <nav className="flex-1 overflow-y-auto py-4">
          {NAV.map((group) => (
            <div key={group.title} className="px-3 mb-5">
              <div className="px-3 mb-1 text-[11px] font-semibold uppercase tracking-wider text-gray-400">
                {group.title}
              </div>
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  className={({ isActive }) =>
                    `flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                      isActive
                        ? 'bg-gray-900 text-white'
                        : 'text-gray-600 hover:bg-gray-100 hover:text-gray-900'
                    }`
                  }
                >
                  <item.icon size={17} strokeWidth={2} />
                  {item.label}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="px-6 py-3 border-t border-gray-100 text-xs text-gray-400">
          api.l1beat.io
        </div>
      </aside>

      {/* Main column */}
      <div className="ml-60 flex flex-col min-h-screen">
        {/* Topbar */}
        <header className="sticky top-0 z-40 bg-white/80 backdrop-blur-xl border-b border-gray-200 px-6 py-3 flex items-center gap-4">
          <form onSubmit={handleSearch} className="flex items-center gap-2 flex-1 max-w-xl">
            <Search size={16} className="text-gray-400 flex-shrink-0" />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search block #, tx hash, or address…"
              className="flex-1 bg-transparent text-sm text-gray-800 placeholder-gray-400 focus:outline-none"
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
            />
          </form>

          {/* Chain switcher — drives the explorer + search target chain */}
          {evmChains.length > 0 && (
            <select
              value={currentChain}
              onChange={(e) => navigate(`/evm/${e.target.value}/blocks`)}
              className="text-sm border border-gray-300 rounded-lg px-2 py-1.5 bg-white text-gray-700 focus:outline-none focus:ring-2 focus:ring-gray-900/10 cursor-pointer"
              title="Switch chain"
            >
              {/* Ensure the current chain is selectable even if not in the synced list */}
              {!evmChains.some((c) => c.chain_id === currentChain) && (
                <option value={currentChain}>Chain {currentChain}</option>
              )}
              {evmChains.map((c) => (
                <option key={c.chain_id} value={c.chain_id}>
                  {c.name || `Chain ${c.chain_id}`}
                </option>
              ))}
            </select>
          )}

          <HealthBadge />
        </header>

        {/* Content */}
        <main className="flex-1 p-6 max-w-7xl w-full mx-auto">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

export default Layout;
