import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiRequest } from "@/lib/queryClient";
import { type ProcessingJob } from "@shared/schema";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useToast } from "@/hooks/use-toast";
import { Download, Trash2, FileText, CheckCircle2, Clock, AlertCircle } from "lucide-react";
import { useEffect } from "react";

export default function ProcessingQueue() {
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const { data: jobs = [], isLoading } = useQuery<ProcessingJob[]>({
    queryKey: ["/api/jobs"],
    refetchInterval: 2000, // Poll every 2 seconds for updates
  });

  const deleteMutation = useMutation({
    mutationFn: async (jobId: string) => {
      await apiRequest("DELETE", `/api/jobs/${jobId}`);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["/api/jobs"] });
      toast({
        title: "Job deleted",
        description: "Processing job has been removed",
      });
    },
  });

  const downloadFile = async (job: ProcessingJob) => {
    try {
      const response = await fetch(`/api/download/${job.id}`);
      if (!response.ok) throw new Error("Download failed");
      
      const blob = await response.blob();
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `processed_${job.fileName}`;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
      
      toast({
        title: "Download started",
        description: "Your processed file is downloading",
      });
    } catch (error) {
      toast({
        title: "Download failed",
        description: "Could not download the processed file",
        variant: "destructive",
      });
    }
  };

  if (isLoading) {
    return (
      <Card>
        <CardContent className="p-6">
          <div className="flex items-center space-x-4">
            <div className="w-10 h-10 bg-muted rounded-lg animate-pulse" />
            <div className="space-y-2 flex-1">
              <div className="h-4 bg-muted rounded animate-pulse" />
              <div className="h-3 bg-muted rounded w-2/3 animate-pulse" />
            </div>
          </div>
        </CardContent>
      </Card>
    );
  }

  if (jobs.length === 0) {
    return (
      <Card>
        <CardContent className="p-12 text-center">
          <FileText className="w-16 h-16 text-muted-foreground mx-auto mb-4" />
          <h3 className="text-lg font-semibold text-foreground mb-2">No files in queue</h3>
          <p className="text-muted-foreground">Upload files to start processing</p>
        </CardContent>
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between">
            <span>Processing Queue</span>
            <Badge variant="secondary" data-testid="queue-count">
              {jobs.length} files
            </Badge>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {jobs.map((job) => (
            <div
              key={job.id}
              className={`flex items-center justify-between p-4 rounded-lg border ${
                job.status === 'completed' 
                  ? 'bg-accent/10 border-accent/20' 
                  : job.status === 'failed'
                  ? 'bg-destructive/10 border-destructive/20'
                  : 'bg-muted/50'
              }`}
              data-testid={`job-${job.id}`}
            >
              <div className="flex items-center space-x-4 flex-1">
                <div className={`w-10 h-10 rounded-lg flex items-center justify-center ${
                  job.status === 'completed' 
                    ? 'bg-accent/20' 
                    : job.status === 'failed'
                    ? 'bg-destructive/20'
                    : 'bg-primary/20'
                }`}>
                  {job.status === 'completed' ? (
                    <CheckCircle2 className="w-5 h-5 text-accent" />
                  ) : job.status === 'failed' ? (
                    <AlertCircle className="w-5 h-5 text-destructive" />
                  ) : (
                    <Clock className="w-5 h-5 text-primary" />
                  )}
                </div>
                
                <div className="flex-1">
                  <p className="font-medium text-foreground" data-testid={`job-filename-${job.id}`}>
                    {job.fileName}
                  </p>
                  <p className="text-sm text-muted-foreground">
                    {job.status === 'completed' ? (
                      `Converted using ${job.toolType} • ${job.fileSize}`
                    ) : job.status === 'failed' ? (
                      `Failed to process • ${job.fileSize}`
                    ) : job.status === 'processing' ? (
                      `Processing with ${job.toolType} • ${job.fileSize}`
                    ) : (
                      `Waiting to process • ${job.fileSize}`
                    )}
                  </p>
                </div>
              </div>

              <div className="flex items-center space-x-3">
                {job.status === 'processing' && (
                  <div className="w-32">
                    <Progress 
                      value={parseInt(job.progress || "0")} 
                      className="w-full"
                      data-testid={`job-progress-${job.id}`}
                    />
                    <span className="text-xs text-muted-foreground ml-2">
                      {job.progress}%
                    </span>
                  </div>
                )}
                
                {job.status === 'completed' && (
                  <Button
                    size="sm"
                    className="bg-accent text-accent-foreground hover:bg-accent/90"
                    onClick={() => downloadFile(job)}
                    data-testid={`download-button-${job.id}`}
                  >
                    <Download className="w-4 h-4 mr-2" />
                    Download
                  </Button>
                )}

                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => deleteMutation.mutate(job.id)}
                  disabled={deleteMutation.isPending}
                  data-testid={`delete-button-${job.id}`}
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  );
}
