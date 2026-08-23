// Worker module for parallel_sum.ts — compiled into that example's binary
// via new Worker('./sum_worker.ts'), never as a standalone entry.
import { parentPort, workerData } from 'worker_threads';

const label: string = workerData;

parentPort.on('message', (nums: number[]) => {
  let sum = 0;
  for (const n of nums) {
    sum += n;
  }
  console.log(label + " summed " + nums.length + " numbers");
  parentPort.postMessage(sum);
});
