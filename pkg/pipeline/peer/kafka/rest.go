package kafka

// Unsure if REST stuff should be here
//
// type Request struct {
// 	Message string `json:"message"`
// 	Topic   string `json:"topic"`
// }

// type TopicResponse struct {
// 	Detail sarama.TopicDetail `json:"detail"`
// 	Name   string             `json:"name"`
// }

// type ACLRequest struct {
// 	ResourceName        string                        `json:"resource_name"`
// 	Principal           string                        `json:"principal"`
// 	Host                string                        `json:"host"`
// 	ResourceType        sarama.AclResourceType        `json:"resource_type"`
// 	ResourcePatternType sarama.AclResourcePatternType `json:"resource_pattern_type"`
// 	Operation           sarama.AclOperation           `json:"operation"`
// 	PermissionType      sarama.AclPermissionType      `json:"permission_type"`
// }

// type ACLFilterRequest struct {
// 	ResourceName        string                        `json:"resource_name"`
// 	Principal           string                        `json:"principal"`
// 	Host                string                        `json:"host"`
// 	ResourceType        sarama.AclResourceType        `json:"resource_type"`
// 	ResourcePatternType sarama.AclResourcePatternType `json:"resource_pattern_type"`
// 	Operation           sarama.AclOperation           `json:"operation"`
// 	PermissionType      sarama.AclPermissionType      `json:"permission_type"`
// }

/*
// HTTPServer exposes Kafka operations via HTTP API
func HTTPServer() {
	r := pigo.NewRouter()

	r.Use(mw.RequestID)
	r.Use(mw.LoggerWithOptions(nil))
	r.Use(mw.CORSWithOptions(nil))

	// API to list topics
	r.Handle("GET /topics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		topics, err := ListTopics(kafkaConfig)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
		}

		var response []TopicResponse
		for name, detail := range topics {
			response = append(response, TopicResponse{
				Name:   name,
				Detail: detail,
			})
		}

		pigo.JSON(w, http.StatusOK, response)
	}))

	// API to create a topic
	r.Handle("POST /topics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TopicName string             `json:"topic_name"`
			Detail    sarama.TopicDetail `json:"detail"`
		}
		if err := pigo.BindOrError(r, w, &req); err != nil {
			return
		}
		err := CreateTopic(kafkaConfig, req.TopicName, req.Detail)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
		}

		pigo.JSON(w, http.StatusOK, "topic created")
	}))

	// API to produce a message
	r.Handle("POST /produce", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req KafkaRequest
		if err := pigo.BindOrError(r, w, &req); err != nil {
			return
		}
		producer, err := CreateProducer(kafkaConfig)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer producer.Close()

		err = ProduceMessage(producer, req.Topic, []byte(req.Message))
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		pigo.JSON(w, http.StatusOK, "Message produced")
	}))

	// API to consume messages from a topic
	r.Handle("GET /consume", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		topic := r.URL.Query().Get("topic")
		if topic == "" {
			pigo.Error(w, http.StatusBadRequest, "topic query parameter is required")
			return
		}

		consumer, err := CreateConsumer(kafkaConfig)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer consumer.Close()

		partitionConsumer, err := consumer.ConsumePartition(topic, 0, sarama.OffsetOldest)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer partitionConsumer.Close()

		var messages []string
		msgCount := 0
		for msg := range partitionConsumer.Messages() {
			messages = append(messages, string(msg.Value))
			msgCount++
			if msgCount >= 10 {
				break
			}
		}

		pigo.JSON(w, http.StatusOK, messages)
	}))

	// API to create an ACL
	r.Handle("POST /acls", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ACLRequest
		if err := pigo.BindOrError(r, w, &req); err != nil {
			return
		}

		resource := sarama.Resource{
			ResourceType:        req.ResourceType,
			ResourceName:        req.ResourceName,
			ResourcePatternType: req.ResourcePatternType,
		}

		acl := sarama.Acl{
			Principal:      req.Principal,
			Host:           req.Host,
			Operation:      req.Operation,
			PermissionType: req.PermissionType,
		}

		err := CreateACL(kafkaConfig, resource, acl)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}
		pigo.JSON(w, http.StatusOK, "ACL created")
	}))

	// API to list ACLs
	r.Handle("GET /acls", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ACLFilterRequest
		if err := pigo.BindOrError(r, w, &req); err != nil {
			return
		}

		filter := sarama.AclFilter{
			ResourceType:              req.ResourceType,
			ResourceName:              &req.ResourceName,
			ResourcePatternTypeFilter: req.ResourcePatternType,
			Principal:                 &req.Principal,
			Host:                      &req.Host,
			Operation:                 req.Operation,
			PermissionType:            req.PermissionType,
		}

		acls, err := ListAcls(kafkaConfig, filter)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}

		pigo.JSON(w, http.StatusOK, acls)
	}))

	// API to delete an ACL
	r.Handle("DELETE /acls", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ACLFilterRequest
		if err := pigo.BindOrError(r, w, &req); err != nil {
			return
		}

		filter := sarama.AclFilter{
			ResourceType:              req.ResourceType,
			ResourceName:              &req.ResourceName,
			Principal:                 &req.Principal,
			Host:                      &req.Host,
			Operation:                 req.Operation,
			PermissionType:            req.PermissionType,
			ResourcePatternTypeFilter: req.ResourcePatternType,
		}

		matchingACLs, err := DeleteACL(kafkaConfig, filter)
		if err != nil {
			pigo.Error(w, http.StatusInternalServerError, err.Error())
			return
		}

		pigo.JSON(w, http.StatusOK, matchingACLs)
	}))

	// Start server

	// Run server in a goroutine
	go func() {
		if err := r.ListenAndServe(fmt.Sprintf(":%d", 8080)); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	fmt.Printf("Server is running on port %d\n", 8080)

	// Set up signal handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Wait for SIGINT or SIGTERM
	<-stop

	fmt.Println("Shutting down server...")

	// Create a deadline for the shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Attempt graceful shutdown
	if err := r.Shutdown(ctx); err != nil {
		fmt.Printf("server forced to shutdown: %s", err)
	}
	fmt.Println("Server gracefully stopped")
}
*/
