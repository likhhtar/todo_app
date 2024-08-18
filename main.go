package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/thedevsaddam/renderer"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

var (
	rnd    *renderer.Render
	client *mongo.Client
	db     *mongo.Database
)

const (
	dbName         string = "golang-todo"
	collectionName string = "todo"
)

type (
	TodoModel struct {
		ID        primitive.ObjectID `bson:"id,omitempty"`
		Title     string             `bson:"title"`
		Completed bool               `bson:"completed"`
		CreatedAt time.Time          `bson:"created_at"`
	}

	Todo struct {
		ID        string    `json:"id"`
		Title     string    `json:"title"`
		Completed bool      `json:"completed"`
		CreatedAt time.Time `json:"created_at"`
	}

	GetTodoResponse struct {
		Message string `json:"message"`
		Data    []Todo `json:"data"`
	}

	CreateTodo struct {
		Title string `json:"title"`
	}

	UpdateTodo struct {
		Title     string `json:"title"`
		Completed bool   `json:"completed"`
	}
)

func init() {
	fmt.Println("Initializing application...")

	// Инициализация рендерера
	rnd = renderer.New(
		renderer.Options{
			ParseGlobPattern: "html/*.html",
		},
	)

	// Создание контекста с тайм-аутом
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Подключение к MongoDB
	var err error
	client, err = mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	checkError(err)

	// Проверка доступности MongoDB
	err = client.Ping(ctx, readpref.Primary())
	checkError(err)

	// Инициализация базы данных
	db = client.Database(dbName)
	fmt.Printf("Database %s initialized.\n", dbName)
}

func main() {
	// Настройка маршрутизатора (управляет http запросами)
	router := chi.NewRouter()
	router.Use(middleware.Logger)

	// Обработка статических файлов
	fs := http.FileServer(http.Dir("./static"))
	router.Handle("/static/*", http.StripPrefix("/static/", fs))

	// Маршрутизация и обработчики
	router.Get("/", homeHandler)
	router.Mount("/todo", todoHandlers())

	// Настройка и запуск сервера
	server := &http.Server{
		Addr:         ":9000",
		Handler:      router,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	// Обработка сигналов прерывания
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt)

	// Запуск сервера
	go func() {
		fmt.Println("Server started on port", server.Addr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("listen:%s\n", err)
		}
	}()

	// Завершение программы
	sig := <-stopChan
	log.Printf("Signal received: %v. Shutting down...", sig)

	if err := client.Disconnect(context.Background()); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v\n", err)
	}

	log.Println("Server shutdown successfully")
}

func todoHandlers() http.Handler {
	router := chi.NewRouter()

	// Определяем группу маршрутов для операций с задачами
	router.Group(func(r chi.Router) {
		r.Get("/", getTodos)
		r.Post("/", createTodo)
		r.Put("/{id}", updateTodo)
		r.Delete("/{id}", deleteTodo)
	})

	return router
}

func checkError(err error) {
	if err != nil {
		log.Fatalf("An error occurred: %v/n", err)
	}
}

func homeHandler(rw http.ResponseWriter, r *http.Request) {
	// filePath := "./README.md"
	err := rnd.HTML(rw, http.StatusOK, "indexPage", nil)
	checkError(err)
}

func getTodos(rw http.ResponseWriter, r *http.Request) {
	// Определяем фильтр для поиска задач
	filter := bson.D{}

	// Выполняем запрос к базе данных
	cursor, err := db.Collection(collectionName).Find(context.Background(), filter)
	if err != nil {
		log.Printf("failed to fetch todo records from the db: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "Could not fetch the todo collection",
			"error":   err.Error(),
		})
		return
	}
	defer cursor.Close(context.Background())

	// Читаем данные из курсора
	var todoListFromDB = []TodoModel{}
	if err = cursor.All(context.Background(), &todoListFromDB); err != nil {
		checkError(err)
	}

	// Преобразуем задачи из модели в формат ответа
	todoList := []Todo{}
	for _, td := range todoListFromDB {
		todoList = append(todoList, Todo{
			ID:        td.ID.Hex(),
			Title:     td.Title,
			Completed: td.Completed,
			CreatedAt: td.CreatedAt,
		})
	}
	rnd.JSON(rw, http.StatusOK, GetTodoResponse{
		Message: "All todos retrieved",
		Data:    todoList,
	})
}

func createTodo(rw http.ResponseWriter, r *http.Request) {
	var todoReq CreateTodo

	// Декодируем данные из тела запроса
	if err := json.NewDecoder(r.Body).Decode(&todoReq); err != nil {
		log.Printf("failed to decode JSON data: %v\n", err)
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "could not decode data",
		})
		return
	}

	if todoReq.Title == "" {
		log.Println("no title added to response body")
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "please add a title",
		})
		return
	}

	// Создаем модель задачи для добавления в базу данных
	todoModel := TodoModel{
		ID:        primitive.NewObjectID(),
		Title:     todoReq.Title,
		Completed: false,
		CreatedAt: time.Now(),
	}

	// Добавляем задачу в базу данных
	result, err := db.Collection(collectionName).InsertOne(r.Context(), todoModel)
	if err != nil {
		log.Printf("failed to insert data into the database: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "failed to insert data into the database",
			"error":   err.Error(),
		})
		return
	}

	// Отправляем успешный ответ клиенту
	rnd.JSON(rw, http.StatusCreated, renderer.M{
		"message": "Todo created successfully",
		"ID":      result.InsertedID,
	})
}

func updateTodo(rw http.ResponseWriter, r *http.Request) {
	// Достаем id из URL параметра
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		log.Printf("id is not a valid hex value: %v\n", err.Error())
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "The id is invalid",
			"error":   err.Error(),
		})
		return
	}

	// Декодируем данные обновления из тела запроса
	var updateTodoReq UpdateTodo
	if err := json.NewDecoder(r.Body).Decode(&updateTodoReq); err != nil {
		log.Printf("failed to decode the json responce body data: %v\n", err.Error())
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "could not decode the data",
			"error":   err.Error(),
		})
	}

	if updateTodoReq.Title == "" {
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "title cannot be empty",
		})
		return
	}

	// Определяем фильтр и обновление для базы данных
	filter := bson.M{"id": objID}
	update := bson.M{"$set": bson.M{
		"title":     updateTodoReq.Title,
		"completed": updateTodoReq.Completed,
	}}

	// Выполняем обновление записи в базе данных
	result, err := db.Collection(collectionName).UpdateOne(r.Context(), filter, update)
	if err != nil {
		log.Printf("failed to update db collection: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "failed to update data in db",
			"error":   err.Error(),
		})
		return
	}

	// Отправляем успешный ответ клиенту
	rnd.JSON(rw, http.StatusOK, renderer.M{
		"message":     "Updated successfully",
		"resultCount": result.ModifiedCount,
	})
}

func deleteTodo(rw http.ResponseWriter, r *http.Request) {
	// Достаем id из URL параметра
	id := chi.URLParam(r, "id")
	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		log.Printf("invalid id: %v\n", err.Error())
		rnd.JSON(rw, http.StatusBadRequest, renderer.M{
			"message": "the ID is invalid",
			"error":   err.Error(),
		})
		return
	}

	// Определяем фильтр и удаляем запись из бд
	filter := bson.M{"id": objID}
	result, err := db.Collection(collectionName).DeleteOne(r.Context(), filter)
	if err != nil {
		log.Printf("could not delete from db: %v\n", err.Error())
		rnd.JSON(rw, http.StatusInternalServerError, renderer.M{
			"message": "error with deleting data",
			"error":   err.Error(),
		})

		return
	}

	// Проверяем, была ли удалена хотя бы одна запись
	if result.DeletedCount == 0 {
		rnd.JSON(rw, http.StatusNotFound, renderer.M{
			"message": "Todo not found",
		})
		return
	}

	// Отправляем успешный ответ клиенту
	rnd.JSON(rw, http.StatusOK, renderer.M{
		"message":    "Item deleted successfully",
		"resultCont": result.DeletedCount,
	})

}
