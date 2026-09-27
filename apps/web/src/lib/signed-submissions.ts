import type { SignedAction, SigningRequest, SubmissionResult } from '@/types/signing'

export interface PreparedSignedAction {
  request: SigningRequest
  signed: SignedAction
}

export interface SettledSignedAction extends PreparedSignedAction {
  outcome: PromiseSettledResult<SubmissionResult>
}

export async function submitSignedActionsConcurrently(
  actions: PreparedSignedAction[],
  submit: (signed: SignedAction) => Promise<SubmissionResult>,
): Promise<SettledSignedAction[]> {
  const outcomes = await Promise.allSettled(actions.map(({ signed }) => submit(signed)))
  return actions.map((action, index) => ({ ...action, outcome: outcomes[index] }))
}
