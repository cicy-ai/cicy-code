// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import {StrictMode} from 'react';
import {createRoot} from 'react-dom/client';
import App from './App.tsx';
import './i18n';
import './index.css';
import {applyCicyTheme, getCicyTheme} from './lib/theme';

applyCicyTheme(getCicyTheme());

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
