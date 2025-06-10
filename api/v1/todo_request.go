package V1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

type Todo struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

var TODOS []Todo // initialize the slice to store todos

func AddTodoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var newTodo Todo
	err := json.NewDecoder(r.Body).Decode(&newTodo)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	newTodo.ID = len(TODOS) + 1
	TODOS = append(TODOS, newTodo)

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newTodo)
}

func ListTodosHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TODOS)
}

func DeleteTodoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	todoID, err := strconv.Atoi(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "Invalid todo ID", http.StatusBadRequest)
		return
	}

	for i, todo := range TODOS {
		if todo.ID == todoID {
			TODOS = append(TODOS[:i], TODOS[i+1:]...)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "Todo with ID %d deleted", todoID)
			return
		}
	}

	http.Error(w, "Todo not found", http.StatusNotFound)
}

func DeleteTodo(id int) error {
	url := fmt.Sprintf("http://localhost:8080/todos/delete?id=%d", id)
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete todo: status %d", resp.StatusCode)
	}

	return nil
}

func ListTodos() ([]Todo, error) {
	resp, err := http.Get("http://localhost:8080/todos/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var todos []Todo
	err = json.NewDecoder(resp.Body).Decode(&todos)
	if err != nil {
		return nil, err
	}

	return todos, nil
}

func AddTodo(title string) (*Todo, error) {
	newTodo := Todo{Title: title}

	body, err := json.Marshal(newTodo)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal todo: %w", err)
	}

	resp, err := http.Post("http://localhost:8080/todos/add", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to send POST request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("API responded with status: %d", resp.StatusCode)
	}

	var createdTodo Todo
	err = json.NewDecoder(resp.Body).Decode(&createdTodo)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &createdTodo, nil
}

func Request() *http.ServeMux {

	mux := http.NewServeMux()

	mux.HandleFunc("/todos/add", AddTodoHandler)
	mux.HandleFunc("/todos/list", ListTodosHandler)
	mux.HandleFunc("/todos/delete", DeleteTodoHandler)

	return mux
}