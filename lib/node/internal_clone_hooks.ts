// The host objects a message to another thread carries by reference
// (worker_threads' clone): a module that has one registers how it crosses,
// so the clone needs none of them.

// A host object in transit: its kind and what makes it again.
export class HostToken {
    tag: string;
    data: any[];
    constructor(tag: string, data: any[]) {
        this.tag = tag;
        this.data = data;
    }
}

// Each registered module's token for a value it owns, or undefined.
export const hostCloners: ((v: any) => HostToken | undefined)[] = [];
// Each tag's object made again on the receiving thread.
export const hostRevivers: Map<string, (data: any[]) => any> = new Map<string, (data: any[]) => any>();
