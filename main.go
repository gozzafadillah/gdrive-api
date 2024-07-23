package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	echo "github.com/labstack/echo/v4"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// ServiceAccount initializes and returns an authenticated HTTP client
func ServiceAccount(secretFile string) *http.Client {
	b, err := os.ReadFile(secretFile)
	if err != nil {
		log.Fatal("error while reading the credential file", err)
	}
	var s = struct {
		Email      string `json:"client_email"`
		PrivateKey string `json:"private_key"`
	}{}
	json.Unmarshal(b, &s)
	config := &jwt.Config{
		Email:      s.Email,
		PrivateKey: []byte(s.PrivateKey),
		Scopes: []string{
			drive.DriveScope,
		},
		TokenURL: google.JWTTokenURL,
	}
	client := config.Client(context.Background())
	return client
}

// createFile uploads a file to Google Drive
func createFile(service *drive.Service, name string, mimeType string, content io.Reader, parentId string) (*drive.File, error) {
	f := &drive.File{
		MimeType: mimeType,
		Name:     name,
		Parents:  []string{parentId},
	}
	file, err := service.Files.Create(f).Media(content).Do()

	if err != nil {
		log.Println("Could not create file: " + err.Error())
		return nil, err
	}

	return file, nil
}

// uploadFileHandler handles the file upload and Google Drive upload
func uploadFileHandler(c echo.Context) error {
	// Read the folder ID from the form
	folderId := c.FormValue("folder")
	if folderId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Folder ID is required"})
	}

	// Read the file from the form
	file, err := c.FormFile("file")
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Failed to read file from request"})
	}

	// Open the file
	src, err := file.Open()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to open file"})
	}
	defer src.Close()

	// Get the Google Drive service
	client := ServiceAccount(".info_source.json")
	srv, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create Google Drive service"})
	}

	// check name file if exist upload update new file
	r, err := srv.Files.List().Q("name='" + file.Filename + "'").Fields("files(id, name, webViewLink)").Do()
	if err != nil {
		log.Printf("Google Drive API error: %v\n", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to list files in Google Drive"})
	}

	if len(r.Files) > 0 {
		// Delete the file
		err = srv.Files.Delete(r.Files[0].Id).Do()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to delete file from Google Drive"})
		}
	}

	// Create the file and upload it with web view link
	uploadedFile, err := createFile(srv, file.Filename, file.Header.Get("Content-Type"), src, folderId)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to upload file to Google Drive"})
	}

	// get file metadata by ID
	getFile, err := srv.Files.Get(uploadedFile.Id).Fields("id, name, webViewLink").Do()
	if err != nil {
		log.Printf("Google Drive API error: %v\n", err)
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to get file metadata from Google Drive"})
	}

	// Return the file ID, name, and web view link
	data := map[string]interface{}{
		"file_id":   getFile.Id,
		"file_name": getFile.Name,
		"file_url":  getFile.WebViewLink,
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "File successfully uploaded",
		"data":    data,
	})
}

// listFilesHandler handles the listing of files in a Google Drive folder
func listFilesHandler(c echo.Context) error {
	// Read the folder ID from the query parameter
	folderId := c.QueryParam("folder")
	if folderId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Folder ID is required"})
	}

	// Get the Google Drive service
	client := ServiceAccount(".info_source.json")
	srv, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create Google Drive service"})
	}

	// List files in the specified folder
	r, err := srv.Files.List().Q("'" + folderId + "' in parents").Fields("files(id, name, webViewLink)").Do()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to list files in Google Drive"})
	}

	data := map[string]interface{}{
		"folder_id": folderId,
		"files":     r.Files,
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"message": "Files successfully retrieved",
		"data":    data,
	})
}

// getFileHandler handles the downloading of files from Google Drive
func getFileHandler(c echo.Context) error {
	// Read the file ID from the path parameter
	fileId := c.Param("fileId")
	if fileId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "File ID is required"})
	}

	// Get the Google Drive service
	client := ServiceAccount(".info_source.json")
	srv, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create Google Drive service"})
	}

	// Get the file
	file, err := srv.Files.Get(fileId).Download()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to get file from Google Drive"})
	}
	defer file.Body.Close()

	return c.Stream(http.StatusOK, file.Header.Get("Content-Type"), file.Body)
}

// getFileMetadataHandler handles getting metadata of files from Google Drive
func getFileMetadataHandler(c echo.Context) error {
	// Define request body struct
	type requestBody struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
	}

	var body requestBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
	}

	// Get the Google Drive service
	client := ServiceAccount(".info_source.json")
	srv, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create Google Drive service"})
	}

	// Get the file metadata by ID
	if body.FileID != "" {
		file, err := srv.Files.Get(body.FileID).Fields("id, name, webViewLink").Do()
		if err != nil {
			log.Printf("Google Drive API error: %v\n", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to get file metadata from Google Drive"})
		}
		data := map[string]interface{}{
			"file_id":   file.Id,
			"file_name": file.Name,
			"file_url":  file.WebViewLink,
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "File metadata successfully retrieved",
			"data":    data,
		})
	}

	if body.FileName != "" {
		// List files in the root folder
		r, err := srv.Files.List().Q("name='" + body.FileName + "'").Fields("files(id, name, webViewLink)").Do()
		if err != nil {
			log.Printf("Google Drive API error: %v\n", err)
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to list files in Google Drive"})
		}

		if len(r.Files) == 0 {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "File not found"})
		}

		data := map[string]interface{}{
			"file_id":   r.Files[0].Id,
			"file_name": r.Files[0].Name,
			"file_url":  r.Files[0].WebViewLink,
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"message": "File metadata successfully retrieved",
			"data":    data,
		})
	}

	// Return an error if neither FileID nor FileName is provided
	return c.JSON(http.StatusBadRequest, map[string]string{"error": "File ID or File Name is required"})
}

// deleteFileHandler handles the deletion of files from Google Drive
func deleteFileHandler(c echo.Context) error {
	// Read the file ID from the path parameter
	fileId := c.Param("fileId")
	if fileId == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "File ID is required"})
	}

	// Get the Google Drive service
	client := ServiceAccount(".info_source.json")
	srv, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to create Google Drive service"})
	}

	// Delete the file
	err = srv.Files.Delete(fileId).Do()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Failed to delete file from Google Drive"})
	}

	return c.JSON(http.StatusOK, map[string]string{"message": "File successfully deleted", "file_id": fileId})
}

func main() {
	e := echo.New()

	// Define the upload endpoint
	e.POST("/upload/file", uploadFileHandler)
	// Define the list files endpoint
	e.GET("/list", listFilesHandler)
	// Define the get file endpoint with path parameter
	e.GET("/download/:fileId", getFileHandler)
	// Define the get file metadata endpoint with path parameter
	e.POST("/file/metadata", getFileMetadataHandler)
	// Define the delete file endpoint with path parameter
	e.DELETE("/file/delete/:fileId", deleteFileHandler)

	// Start the server
	e.Logger.Fatal(e.Start(":8082"))
}
