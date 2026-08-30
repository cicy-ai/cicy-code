// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup, configure } from '@testing-library/react';

// These suites mount whole panels and run under a full-suite fan-out, on CI and
// on dev boxes that are busy with other work. The 1s default gives findBy and
// waitFor no room there: the assertion is right, the render just had not been
// scheduled yet, and the suite fails only in a full run. One budget for every
// file, kept well under vite.config.ts's testTimeout so the two never race.
configure({ asyncUtilTimeout: 10000 });

// Unmount React trees between tests so each test starts from a clean DOM.
afterEach(() => {
  cleanup();
});
