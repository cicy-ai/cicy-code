import React from 'react';
import { LoginForm } from '../components/LoginForm';
import { useAuth } from '../contexts/AuthContext';

const LoginPage: React.FC = () => {
  const { login } = useAuth();
  return <LoginForm onLogin={login} />;
};

export default LoginPage;
