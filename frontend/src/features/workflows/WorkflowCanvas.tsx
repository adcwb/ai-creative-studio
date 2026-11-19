import { ReactFlow, Background, Controls, Handle, Position } from '@xyflow/react'
import type { Node, NodeChange, NodeProps, Viewport } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useI18n } from '../../i18n'
import { labels, portLabels, ports, states } from './types'
import type { Step, Wire } from './types'

export type WorkflowFlowNode = Node<{ step: Step; status?: string }, 'creative'>

function FlowNode({ data: { step, status } }: NodeProps<WorkflowFlowNode>) {
  const { t } = useI18n()
  const inputPorts = ports(step.kind)
  return (
    <div className={'workflow-node workflow-node-' + step.kind}>
      <span className="eyebrow">{t(labels[step.kind])}</span>
      <strong>{step.label || t(labels[step.kind])}</strong>
      {status && <span className="workflow-node-state">{t(states[status] ?? status)}</span>}
      {(step.kind === 'text' || step.kind === 'template') && <p>{step.text?.slice(0, 70)}</p>}
      {inputPorts.map((input, i) => (
        <Handle
          key={input}
          id={input}
          type="target"
          position={Position.Left}
          style={{ top: inputPorts.length === 1 ? '50%' : `${35 + i * 35}%` }}
          title={t(portLabels[input])}
        />
      ))}
      {step.kind === 'variable' && <p>{step.variable_name}</p>}
      {step.kind !== 'output' && <Handle type="source" position={Position.Right} id="output" />}
    </div>
  )
}
const nodeTypes = { creative: FlowNode }

type WorkflowCanvasProps = {
  nodes: WorkflowFlowNode[]
  wires: Wire[]
  locked: boolean
  viewport: Viewport
  fitView: boolean
  onNodesChange: (changes: NodeChange[]) => void
  onSelect: (id: string) => void
  onDragStart: () => void
  onDragEnd: () => void
  onConnect: (edge: Wire) => void
  onViewportChange: (viewport: Viewport) => void
}

export function WorkflowCanvas({
  nodes,
  wires,
  locked,
  viewport,
  fitView,
  onNodesChange,
  onSelect,
  onDragStart,
  onDragEnd,
  onConnect,
  onViewportChange,
}: WorkflowCanvasProps) {
  const { t } = useI18n()
  return (
    <div className="workflow-canvas" aria-label={t('创作流程画布')}>
      <ReactFlow
        nodes={nodes}
        edges={wires.map((wire) => ({
          id: `${wire.source}:${wire.target}:${wire.input}`,
          source: wire.source,
          target: wire.target,
          sourceHandle: 'output',
          targetHandle: wire.input,
        }))}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={(_, node) => onSelect(node.id)}
        onNodeDragStart={onDragStart}
        onNodeDragStop={onDragEnd}
        onConnect={(connection) => {
          if (!locked && connection.source && connection.target && connection.targetHandle) {
            onConnect({
              source: connection.source,
              target: connection.target,
              input: connection.targetHandle,
            })
          }
        }}
        nodesDraggable={!locked}
        nodesConnectable={!locked}
        deleteKeyCode={locked ? null : ['Backspace', 'Delete']}
        defaultViewport={viewport}
        fitView={fitView}
        onMoveEnd={(_, nextViewport) => onViewportChange(nextViewport)}
        minZoom={0.25}
        maxZoom={2}
        ariaLabelConfig={{
          'controls.zoomIn.ariaLabel': t('放大画布'),
          'controls.zoomOut.ariaLabel': t('缩小画布'),
          'controls.fitView.ariaLabel': t('适应画布'),
        }}
      >
        <Background />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}
