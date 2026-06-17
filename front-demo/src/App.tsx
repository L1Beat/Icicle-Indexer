import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import Layout from './components/Layout';
import OpsOverview from './pages/OpsOverview';
import ChainDetail from './pages/ChainDetail';
import Metrics from './pages/Metrics';
import SyncStatus from './pages/SyncStatus';
import PChainOverview from './pages/PChainOverview';
import SubnetValidators from './pages/SubnetValidators';
import ValidatorDetails from './pages/ValidatorDetails';
import BlockDetails from './pages/BlockDetails';
import TransactionDetails from './pages/TransactionDetails';
import ApiPlayground from './pages/ApiPlayground';
import EvmBlocks from './pages/EvmBlocks';
import EvmBlockDetail from './pages/EvmBlockDetail';
import EvmTxDetail from './pages/EvmTxDetail';
import EvmAddress from './pages/EvmAddress';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: 1,
    },
  },
});

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<Layout />}>
            <Route index element={<Navigate to="/overview" replace />} />
            <Route path="overview" element={<OpsOverview />} />
            <Route path="chain/:chainId" element={<ChainDetail />} />
            <Route path="evm-metrics" element={<Navigate to="/evm-metrics/43114/7d" replace />} />
            <Route path="evm-metrics/:chainId/:timePeriod" element={<Metrics />} />
            <Route path="evm" element={<Navigate to="/evm/43114/blocks" replace />} />
            <Route path="evm/:chainId/blocks" element={<EvmBlocks />} />
            <Route path="evm/:chainId/block/:number" element={<EvmBlockDetail />} />
            <Route path="evm/:chainId/tx/:hash" element={<EvmTxDetail />} />
            <Route path="evm/:chainId/address/:address" element={<EvmAddress />} />
            <Route path="p-chain/overview" element={<PChainOverview />} />
            <Route path="p-chain/subnet/:subnetId" element={<SubnetValidators />} />
            <Route path="p-chain/subnet/:subnetId/validator/:nodeId" element={<ValidatorDetails />} />
            <Route path="p-chain/block/:blockNumber" element={<BlockDetails />} />
            <Route path="p-chain/tx/:txId" element={<TransactionDetails />} />
            <Route path="sync-status" element={<SyncStatus />} />
            <Route path="api-playground" element={<ApiPlayground />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}

export default App
