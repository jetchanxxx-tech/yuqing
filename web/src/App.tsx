import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider, App as AntApp } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { AuthProvider } from './stores/auth';
import { RequireAuth } from './components/RequireAuth';
import { MainLayout } from './components/MainLayout';
import LoginPage from './pages/LoginPage';
import RegisterPage from './pages/RegisterPage';
import DashboardPage from './pages/DashboardPage';
import AnalysisNewPage from './pages/AnalysisNewPage';
import AnalysisListPage from './pages/AnalysisListPage';
import AnalysisDetailPage from './pages/AnalysisDetailPage';
import ReportHistoryPage from './pages/ReportHistoryPage';
import PlanSelectionPage from './pages/PlanSelectionPage';
import UsagePage from './pages/UsagePage';
import InvoicesPage from './pages/InvoicesPage';
import SettingsPage from './pages/SettingsPage';
import AdminPage from './pages/AdminPage';

const queryClient = new QueryClient();

const theme = {
  token: {
    colorPrimary: '#FF2442',
    borderRadius: 12,
    fontFamily: '"PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif',
  },
};

export default function App() {
  return (
    <ConfigProvider locale={zhCN} theme={theme}>
      <AntApp>
        <QueryClientProvider client={queryClient}>
          <AuthProvider>
            <BrowserRouter>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route path="/register" element={<RegisterPage />} />
                <Route element={<RequireAuth><MainLayout /></RequireAuth>}>
                  <Route path="/" element={<Navigate to="/dashboard" replace />} />
                  <Route path="/dashboard" element={<DashboardPage />} />
                  <Route path="/analyses/new" element={<AnalysisNewPage />} />
                  <Route path="/analyses" element={<AnalysisListPage />} />
                  <Route path="/analyses/:id" element={<AnalysisDetailPage />} />
                  <Route path="/reports" element={<ReportHistoryPage />} />
                  <Route path="/plans" element={<PlanSelectionPage />} />
                  <Route path="/usage" element={<UsagePage />} />
                  <Route path="/invoices" element={<InvoicesPage />} />
                  <Route path="/settings" element={<SettingsPage />} />
                  <Route path="/admin" element={<AdminPage />} />
                </Route>
              </Routes>
            </BrowserRouter>
          </AuthProvider>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}
