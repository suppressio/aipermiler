// main/oneAtATime.ts — a queue that runs async tasks one after the other: a task starts
// when the previous one has settled, a rejected task does not block the next one, and each
// caller gets its own task's result or rejection. Pure (tested in oneAtATime.test.ts).

export function oneAtATime(): <T>(task: () => Promise<T>) => Promise<T> {
  let chain: Promise<unknown> = Promise.resolve();
  return <T>(task: () => Promise<T>): Promise<T> => {
    const next = chain.then(task, task);
    chain = next.catch(() => undefined);
    return next;
  };
}
