import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
export default defineConfig(({command, mode}) => {
  if (command === 'build' && mode === 'legacy-fixtures') throw new Error('Synthetic regression fixtures cannot be built for deployment')
  return {plugins: [react()], server: {port: 5173, strictPort: true}}
})
