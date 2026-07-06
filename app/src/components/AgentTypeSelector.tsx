// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { AgentTypeOption } from '../lib/agentType';
import AgentTypeOptionButton from './AgentTypeOptionButton';

interface Props {
  value?: string;
  onChange: (value: string) => void;
  options: AgentTypeOption[];
  dataId?: string;
  optionDataIdPrefix?: string;
  className?: string;
}

export default function AgentTypeSelector({
  value = '',
  onChange,
  options,
  dataId = 'agent-type-selector',
  optionDataIdPrefix = 'agent-type-option',
  className = 'grid grid-cols-1 gap-2 sm:grid-cols-2',
}: Props) {
  return (
    <div data-id={dataId} className={className}>
      {options.map((option) => (
        <AgentTypeOptionButton
          key={option.value}
          dataId={`${optionDataIdPrefix}-${option.value}`}
          value={option.value}
          label={option.label}
          description={option.description}
          selected={value === option.value}
          onClick={() => onChange(option.value)}
        />
      ))}
    </div>
  );
}
