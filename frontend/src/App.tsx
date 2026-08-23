import {
  BrowserRouter,
  Route,
  Routes,
} from "react-router-dom";

import AuthInitializer from "./components/common/AuthInitializer";
import AuthRoute from "./components/common/AuthRoute";
import ProtectedRoute from "./components/common/ProtectedRoute";
import LoginPage from "./features/auth/pages/LoginPage";
import SetupPage from "./features/auth/pages/SetupPage";
import DataLoggerListPage from "./features/datalogger/pages/DataLoggerListPage";
import DataLoggerDetailPage from "./features/datalogger/pages/DataLoggerDetailPage";
import DataLoggerQueryPage from "./features/datalogger/pages/DataLoggerQueryPage";
import DataLoggerWizardPage from "./features/datalogger/pages/DataLoggerWizardPage";
import DashboardPage from "./features/dashboard/pages/DashboardPage";
import DeviceListPage from "./features/device/pages/DeviceListPage";
import ReportBuilderPage from "./features/report/pages/ReportBuilderPage";
import ReportDetailPage from "./features/report/pages/ReportDetailPage";
import ReportListPage from "./features/report/pages/ReportListPage";
import PluginListPage from "./features/plugin/pages/PluginListPage";
import EnergyWizardPage from "./features/plugin/pages/EnergyWizardPage";
import TagListPage from "./features/tag/pages/TagListPage";
import TagDetailPage from "./features/tag/pages/TagDetailPage";
import TagWizardPage from "./features/tag/pages/TagWizardPage";
import TagLiveStream from "./features/tag/components/TagLiveStream";
import VGatewayDetailPage from "./features/vgateway/pages/VGatewayDetailPage";
import VGatewayFormPage from "./features/vgateway/pages/VGatewayFormPage";
import VGatewayListPage from "./features/vgateway/pages/VGatewayListPage";

function App() {
  return (
    <BrowserRouter>
      <AuthInitializer />
      <TagLiveStream />

      <Routes>
        <Route path="/" element={<AuthRoute mode="entry" />} />
        <Route
          path="/login"
          element={
            <AuthRoute mode="login">
              <LoginPage />
            </AuthRoute>
          }
        />
        <Route
          path="/setup"
          element={
            <AuthRoute mode="setup">
              <SetupPage />
            </AuthRoute>
          }
        />
        <Route
          path="/dashboard"
          element={
            <ProtectedRoute>
              <DashboardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/vgateways"
          element={
            <ProtectedRoute>
              <VGatewayListPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/devices"
          element={
            <ProtectedRoute>
              <DeviceListPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/tags"
          element={
            <ProtectedRoute>
              <TagListPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/tags/new"
          element={
            <ProtectedRoute>
              <TagWizardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/tags/:id"
          element={
            <ProtectedRoute>
              <TagDetailPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/tags/new/:type"
          element={
            <ProtectedRoute>
              <TagWizardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/data-loggers"
          element={
            <ProtectedRoute>
              <DataLoggerListPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/data-loggers/new"
          element={
            <ProtectedRoute>
              <DataLoggerWizardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/data-loggers/:id/edit"
          element={
            <ProtectedRoute>
              <DataLoggerWizardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/data-loggers/:id/query"
          element={
            <ProtectedRoute>
              <DataLoggerQueryPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/data-loggers/:id"
          element={
            <ProtectedRoute>
              <DataLoggerDetailPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/plugins"
          element={
            <ProtectedRoute>
              <PluginListPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/plugins/new/energy"
          element={
            <ProtectedRoute>
              <EnergyWizardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/plugins/:id/configure"
          element={
            <ProtectedRoute>
              <EnergyWizardPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/reports"
          element={
            <ProtectedRoute>
              <ReportListPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/reports/new"
          element={
            <ProtectedRoute>
              <ReportBuilderPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/reports/:id/edit"
          element={
            <ProtectedRoute>
              <ReportBuilderPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/reports/:id"
          element={
            <ProtectedRoute>
              <ReportDetailPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/vgateways/new"
          element={
            <ProtectedRoute>
              <VGatewayFormPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/vgateways/:id"
          element={
            <ProtectedRoute>
              <VGatewayDetailPage />
            </ProtectedRoute>
          }
        />
        <Route
          path="/vgateways/:id/edit"
          element={
            <ProtectedRoute>
              <VGatewayFormPage />
            </ProtectedRoute>
          }
        />
        <Route path="*" element={<AuthRoute mode="entry" />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
