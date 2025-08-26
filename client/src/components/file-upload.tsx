import { useState, useCallback } from "react";
import { useDropzone } from "react-dropzone";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiRequest } from "@/lib/queryClient";
import { useToast } from "@/hooks/use-toast";
import { Upload, CloudUpload, CheckCircle2, AlertCircle } from "lucide-react";

interface FileUploadProps {
  toolType: string;
  onUploadComplete?: (jobId: string) => void;
  maxFiles?: number;
  acceptedFileTypes?: string[];
  className?: string;
}

export default function FileUpload({ 
  toolType, 
  onUploadComplete,
  maxFiles = 1,
  acceptedFileTypes = ['.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx'],
  className 
}: FileUploadProps) {
  const [uploadProgress, setUploadProgress] = useState(0);
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const uploadMutation = useMutation({
    mutationFn: async (formData: FormData) => {
      const response = await apiRequest("POST", "/api/jobs", formData);
      return response.json();
    },
    onSuccess: (data) => {
      toast({
        title: "Upload successful!",
        description: "Your file is being processed.",
      });
      queryClient.invalidateQueries({ queryKey: ["/api/jobs"] });
      onUploadComplete?.(data.id);
      setUploadProgress(0);
    },
    onError: (error) => {
      toast({
        title: "Upload failed",
        description: error instanceof Error ? error.message : "Failed to upload file",
        variant: "destructive",
      });
      setUploadProgress(0);
    },
  });

  const onDrop = useCallback((acceptedFiles: File[]) => {
    if (acceptedFiles.length === 0) return;

    const file = acceptedFiles[0];
    const formData = new FormData();
    formData.append("file", file);
    formData.append("toolType", toolType);

    // Simulate upload progress
    const progressInterval = setInterval(() => {
      setUploadProgress(prev => {
        if (prev >= 90) {
          clearInterval(progressInterval);
          return 90;
        }
        return prev + 10;
      });
    }, 200);

    uploadMutation.mutate(formData);
  }, [toolType, uploadMutation]);

  const { getRootProps, getInputProps, isDragActive, fileRejections } = useDropzone({
    onDrop,
    maxFiles,
    accept: acceptedFileTypes.reduce((acc, type) => {
      acc[type] = [];
      return acc;
    }, {} as Record<string, string[]>),
    maxSize: 50 * 1024 * 1024, // 50MB
  });

  const isUploading = uploadMutation.isPending;
  const isSuccess = uploadMutation.isSuccess;
  const hasErrors = fileRejections.length > 0;

  return (
    <div className={cn("w-full", className)}>
      <div
        {...getRootProps()}
        className={cn(
          "drag-zone border-2 border-dashed rounded-2xl p-12 text-center cursor-pointer transition-all duration-300",
          isDragActive && "drag-over",
          isUploading && "pointer-events-none opacity-75",
          isSuccess && "border-accent bg-accent/5",
          hasErrors && "border-destructive bg-destructive/5",
          "border-border hover:border-primary hover:bg-primary/5"
        )}
        data-testid="file-upload-zone"
      >
        <input {...getInputProps()} data-testid="file-input" />
        
        <div className="flex flex-col items-center space-y-4">
          {isUploading ? (
            <CloudUpload className="w-16 h-16 text-primary animate-pulse" />
          ) : isSuccess ? (
            <CheckCircle2 className="w-16 h-16 text-accent" />
          ) : hasErrors ? (
            <AlertCircle className="w-16 h-16 text-destructive" />
          ) : (
            <Upload className="w-16 h-16 text-muted-foreground" />
          )}
          
          <div>
            {isUploading ? (
              <div className="space-y-2">
                <h3 className="text-2xl font-semibold text-foreground">Uploading...</h3>
                <div className="w-64 mx-auto">
                  <Progress value={uploadProgress} className="w-full" />
                </div>
                <p className="text-muted-foreground">{uploadProgress}% complete</p>
              </div>
            ) : isSuccess ? (
              <div>
                <h3 className="text-2xl font-semibold text-accent">Upload Complete!</h3>
                <p className="text-muted-foreground">Your file is being processed</p>
              </div>
            ) : hasErrors ? (
              <div>
                <h3 className="text-2xl font-semibold text-destructive">Upload Error</h3>
                <p className="text-muted-foreground">
                  {fileRejections[0]?.errors[0]?.message || "File not supported"}
                </p>
              </div>
            ) : isDragActive ? (
              <div>
                <h3 className="text-2xl font-semibold text-primary">Drop files here</h3>
                <p className="text-muted-foreground">Release to upload</p>
              </div>
            ) : (
              <div>
                <h3 className="text-2xl font-semibold text-foreground">Drop files here</h3>
                <p className="text-muted-foreground mb-4">or click to browse</p>
                <Button 
                  type="button"
                  className="bg-primary text-primary-foreground hover:bg-primary/90"
                  data-testid="choose-files-button"
                >
                  Choose Files
                </Button>
              </div>
            )}
          </div>
        </div>
      </div>
      
      <div className="flex flex-wrap justify-center gap-4 text-sm text-muted-foreground mt-4">
        <span><i className="fas fa-shield-alt mr-1"></i>256-bit SSL encryption</span>
        <span><i className="fas fa-clock mr-1"></i>Files deleted after 1 hour</span>
        <span><i className="fas fa-check mr-1"></i>GDPR compliant</span>
      </div>
    </div>
  );
}
