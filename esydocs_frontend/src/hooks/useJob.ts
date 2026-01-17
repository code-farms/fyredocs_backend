import { useState, useCallback, useRef, useEffect } from 'react';
import { Job, JobState, ToolId, ToolOptions } from '@/types';

/**
 * Custom hook for managing job lifecycle.
 * Handles job creation, status polling, and state transitions.
 * 
 * Job State Machine:
 * pending → queued → processing → (completed | failed)
 */

interface UseJobOptions {
  pollingInterval?: number;
  maxPollingAttempts?: number;
  onComplete?: (job: Job) => void;
  onError?: (error: string) => void;
}

interface UseJobReturn {
  job: Job | null;
  createJob: (toolId: ToolId, fileIds: string[], options: ToolOptions) => void;
  cancelJob: () => void;
  retryJob: () => void;
  resetJob: () => void;
  isPolling: boolean;
}

// Simulated job for demo purposes
// In production, this would be replaced with actual API calls via React Query
const simulateJobProgress = (
  job: Job,
  onUpdate: (job: Job) => void,
  onComplete: () => void
) => {
  const steps = ['Uploading files', 'Processing', 'Optimizing', 'Finalizing'];
  let currentStep = 0;
  let progress = 0;

  const interval = setInterval(() => {
    progress += Math.random() * 15 + 5;

    if (progress >= 100) {
      currentStep++;
      progress = 0;

      if (currentStep >= steps.length) {
        clearInterval(interval);
        onUpdate({
          ...job,
          state: 'completed',
          completedAt: new Date(),
          progress: {
            currentStep: 'Complete',
            totalSteps: steps.length,
            completedSteps: steps.length,
            percentage: 100,
          },
          result: {
            downloadUrl: '#',
            fileName: 'processed-document.pdf',
            fileSize: 1024 * 1024 * 2.5,
            expiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000),
          },
        });
        onComplete();
        return;
      }
    }

    const newState: JobState = currentStep === 0 ? 'queued' : 'processing';

    onUpdate({
      ...job,
      state: newState,
      updatedAt: new Date(),
      progress: {
        currentStep: steps[currentStep],
        totalSteps: steps.length,
        completedSteps: currentStep,
        percentage: Math.min(
          Math.round(((currentStep * 100 + progress) / steps.length)),
          99
        ),
        estimatedTimeRemaining: Math.round((steps.length - currentStep) * 3 - (progress / 100) * 3),
      },
    });
  }, 500);

  return () => clearInterval(interval);
};

export const useJob = ({
  pollingInterval = 2000,
  maxPollingAttempts = 300,
  onComplete,
  onError,
}: UseJobOptions = {}): UseJobReturn => {
  const [job, setJob] = useState<Job | null>(null);
  const [isPolling, setIsPolling] = useState(false);
  const pollingRef = useRef<(() => void) | null>(null);
  const attemptsRef = useRef(0);

  const stopPolling = useCallback(() => {
    if (pollingRef.current) {
      pollingRef.current();
      pollingRef.current = null;
    }
    setIsPolling(false);
    attemptsRef.current = 0;
  }, []);

  const createJob = useCallback(
    (toolId: ToolId, fileIds: string[], options: ToolOptions) => {
      const newJob: Job = {
        id: `job-${Date.now()}`,
        toolId,
        state: 'pending',
        progress: {
          currentStep: 'Initializing',
          totalSteps: 4,
          completedSteps: 0,
          percentage: 0,
        },
        createdAt: new Date(),
        updatedAt: new Date(),
        fileIds,
        options,
      };

      setJob(newJob);
      setIsPolling(true);

      // Start simulated progress
      pollingRef.current = simulateJobProgress(
        newJob,
        (updatedJob) => setJob(updatedJob),
        () => {
          stopPolling();
          if (onComplete && job) {
            onComplete(job);
          }
        }
      );
    },
    [stopPolling, onComplete]
  );

  const cancelJob = useCallback(() => {
    stopPolling();
    if (job) {
      setJob({
        ...job,
        state: 'failed',
        error: {
          code: 'CANCELLED',
          message: 'Job was cancelled by user',
          isRetryable: true,
        },
      });
    }
  }, [job, stopPolling]);

  const retryJob = useCallback(() => {
    if (job) {
      createJob(job.toolId, job.fileIds, job.options);
    }
  }, [job, createJob]);

  const resetJob = useCallback(() => {
    stopPolling();
    setJob(null);
  }, [stopPolling]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      stopPolling();
    };
  }, [stopPolling]);

  return {
    job,
    createJob,
    cancelJob,
    retryJob,
    resetJob,
    isPolling,
  };
};
