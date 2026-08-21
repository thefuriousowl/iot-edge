import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
} from "react-router-dom";

import LoginPage from "./features/auth/pages/LoginPage";
import SystemHealthPage from "./features/system/pages/SystemHealthPage";

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<SystemHealthPage />} />
        <Route path="/login" element={<LoginPage />} />
        <Route path="/dashboard" element={<SystemHealthPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
