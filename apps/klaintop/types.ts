// klaintop — the shared state shape. The whole app is a pure function of a
// `State`, so every module that reads or renders it agrees on this one type.

export type Proc = { pid: number; cpu: number; mem: number; comm: string };

export type State = {
  procs: Proc[];
  cursor: number;
  sort: string; // "cpu" | "mem"
  confirming: boolean;
  tick: number;
};
