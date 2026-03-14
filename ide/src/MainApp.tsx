import React from 'react';
import { AuthProvider } from './contexts/AuthContext';
import AppRouter from './router/AppRouter';

const App: React.FC = () => (
  <AuthProvider>
    <AppRouter />
  </AuthProvider>
);

export default App;
