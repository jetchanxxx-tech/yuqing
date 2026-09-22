import { lazy, Suspense } from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ConfigProvider, App as AntApp, Spin } from 'antd';
import zhCN from 'antd/locale/zh_CN';
import { AuthProvider } from './stores/auth';
import { RequireAuth } from './components/RequireAuth';
import { MainLayout } from './components/MainLayout';
import { ApiErrorHandler } from './components/ApiErrorHandler';

// —— 路由级代码分割：按页面懒加载，首屏只加载仪表盘 ——
const LoginPage = lazy(() => import('./pages/LoginPage'));
const RegisterPage = lazy(() => import('./pages/RegisterPage'));
const DashboardPage = lazy(() => import('./pages/DashboardPage'));
const TrendsPage = lazy(() => import('./pages/TrendsPage'));
const AnalysisNewPage = lazy(() => import('./pages/AnalysisNewPage'));
const AnalysisListPage = lazy(() => import('./pages/AnalysisListPage'));
const AnalysisDetailPage = lazy(() => import('./pages/AnalysisDetailPage'));
const ReportHistoryPage = lazy(() => import('./pages/ReportHistoryPage'));
const PlanSelectionPage = lazy(() => import('./pages/PlanSelectionPage'));
const UsagePage = lazy(() => import('./pages/UsagePage'));
const InvoicesPage = lazy(() => import('./pages/InvoicesPage'));
const SettingsPage = lazy(() => import('./pages/SettingsPage'));
const AdminPage = lazy(() => import('./pages/AdminPage'));
const VerifyEmailPage = lazy(() => import('./pages/VerifyEmailPage'));

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 10_000,
    },
  },
});

const theme = {
  token: {
    colorPrimary: '#FF2442',
    borderRadius: 12,
    colorBgLayout: '#f5f5f5',
    fontFamily: '"PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans SC", sans-serif',
    fontSize: 14,
  },
  components: {
    Button: {
      borderRadius: 9999,
      fontWeight: 500,
    },
    Card: {
      borderRadiusLG: 16,
    },
    Layout: {
      siderBg: '#fff',
      headerBg: '#fff',
      headerHeight: 56,
    },
    Menu: {
      itemBorderRadius: 10,
      itemSelectedBg: 'rgba(255, 36, 66, 0.06)',
    },
    Table: {
      headerBg: '#fafafa',
    },
  },
};

function PageFallback() {
  return (
    <div style={{ minHeight: 'calc(100vh - 120px)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Spin size="large" />
    </div>
  );
}

export default function App() {
  return (
    <ConfigProvider locale={zhCN} theme={theme}>
      <AntApp>
        <QueryClientProvider client={queryClient}>
          <AuthProvider>
            <BrowserRouter>
              {/* 监听 api:error 事件统一呈现错误信封 */}
              <ApiErrorHandler />
              <Suspense fallback={<PageFallback />}>
                <Routes>
                  <Route path="/login" element={<LoginPage />} />
                  <Route path="/register" element={<RegisterPage />} />
                  <Route path="/verify-email" element={<VerifyEmailPage />} />
                  <Route element={<RequireAuth><MainLayout /></RequireAuth>}>
                    <Route path="/" element={<Navigate to="/dashboard" replace />} />
                    <Route path="/dashboard" element={<DashboardPage />} />
                    <Route path="/trends" element={<TrendsPage />} />
                    <Route path="/analyses/new" element={<AnalysisNewPage />} />
                    <Route path="/analyses" element={<AnalysisListPage />} />
                    <Route path="/analyses/:id" element={<AnalysisDetailPage />} />
                    <Route path="/reports" element={<ReportHistoryPage />} />
                    <Route path="/plans" element={<PlanSelectionPage />} />
                    <Route path="/usage" element={<UsagePage />} />
                    <Route path="/invoices" element={<InvoicesPage />} />
                    <Route path="/settings" element={<SettingsPage />} />
                    <Route path="/admin" element={<AdminPage />} />
                    <Route path="*" element={<Navigate to="/dashboard" replace />} />
                  </Route>
                </Routes>
              </Suspense>
            </BrowserRouter>
          </AuthProvider>
        </QueryClientProvider>
      </AntApp>
    </ConfigProvider>
  );
}
