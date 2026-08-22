import { describe, expect, it } from 'vitest';
import { buildGeneralSettingsPayload, roleTemplateSelectOptions } from './AgentInspector';

describe('AgentInspector role template settings', () => {
  it('includes the selected role template in the settings PATCH payload', () => {
    const payload = buildGeneralSettingsPayload({
      target: 'w-1:main.0',
      title: 'Agent',
      role_template: 'knowledge-specialist',
    });

    expect(payload.role_template).toBe('knowledge-specialist');
  });

  it('maps the template catalog to select options', () => {
    expect(roleTemplateSelectOptions(['assistant', 'knowledge-specialist'])).toEqual([
      { value: 'assistant', label: 'assistant' },
      { value: 'knowledge-specialist', label: 'knowledge-specialist' },
    ]);
  });
});
