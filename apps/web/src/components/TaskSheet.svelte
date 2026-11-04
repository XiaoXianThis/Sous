<script lang="ts">
  // The task sheet: the full pipeline, every Run and its steps. The workbench
  // keeps the compact rows; this is the old task page in a sheet.
  import Sheet from './Sheet.svelte'
  import TaskView from './TaskView.svelte'

  let { id, onClose }: { id: string; onClose: () => void } = $props()
</script>

<Sheet title="任务" wide {onClose}>
  <TaskView {id} embedded />
</Sheet>
