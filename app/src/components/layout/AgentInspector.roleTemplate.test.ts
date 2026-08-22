import { describe, expect, it } from 'vitest';
import { buildGeneralSettingsPayload, roleTemplateSelectOptions, roleTemplateUpdatePayload } from './AgentInspector';

describe('AgentInspector role template settings', () => {
  it('does not include the role template in unrelated general-settings saves', () => {
    const payload = buildGeneralSettingsPayload({
      target: 'w-1:main.0',
      title: 'Agent',
      role_template: 'knowledge-specialist',
    });

    expect(payload).not.toHaveProperty('role_template');
  });

  it('maps the template catalog to select options', () => {
    expect(roleTemplateSelectOptions(['assistant', 'knowledge-specialist'])).toEqual([
      { value: 'assistant', label: 'assistant' },
      { value: 'knowledge-specialist', label: 'knowledge-specialist' },
    ]);
  });

  it('saves a role template without unrelated general settings', () => {
    expect(roleTemplateUpdatePayload(' knowledge-specialist ')).toEqual({
      role_template: 'knowledge-specialist',
    });
  });
});
