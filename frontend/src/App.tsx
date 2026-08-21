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
import SystemHealthPage from "./features/system/pages/SystemHealthPage";
import VGatewayFormPage from "./features/vgateway/pages/VGatewayFormPage";
import VGatewayListPage from "./features/vgateway/pages/VGatewayListPage";

function App() {
  return (
    <BrowserRouter>
      <AuthInitializer />

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
              <SystemHealthPage />
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
          path="/vgateways/new"
          element={
            <ProtectedRoute>
              <VGatewayFormPage />
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
