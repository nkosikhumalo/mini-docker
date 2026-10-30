--
-- PostgreSQL database dump
--

\restrict b8tc2XSD0qFuHRmg6HcVYSBvdezPZQ33vwKX5glxWhnZMjbqbGC7YExWOeXMoMw

-- Dumped from database version 16.14 (Ubuntu 16.14-0ubuntu0.24.04.1)
-- Dumped by pg_dump version 16.14 (Ubuntu 16.14-0ubuntu0.24.04.1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

ALTER TABLE ONLY public.user_api_keys DROP CONSTRAINT user_api_keys_user_id_fkey;
ALTER TABLE ONLY public.session_questions DROP CONSTRAINT session_questions_session_id_fkey;
ALTER TABLE ONLY public.interview_answers DROP CONSTRAINT interview_answers_session_id_fkey;
DROP INDEX public.idx_users_email;
DROP INDEX public.idx_trials_ip;
DROP INDEX public.idx_sessions_user;
DROP INDEX public.idx_questions_session;
DROP INDEX public.idx_email_verif_email;
DROP INDEX public.idx_apikeys_user;
DROP INDEX public.idx_answers_session;
ALTER TABLE ONLY public.users DROP CONSTRAINT users_pkey;
ALTER TABLE ONLY public.users DROP CONSTRAINT users_email_key;
ALTER TABLE ONLY public.user_api_keys DROP CONSTRAINT user_api_keys_user_id_provider_key;
ALTER TABLE ONLY public.user_api_keys DROP CONSTRAINT user_api_keys_pkey;
ALTER TABLE ONLY public.trials DROP CONSTRAINT trials_pkey;
ALTER TABLE ONLY public.session_questions DROP CONSTRAINT session_questions_pkey;
ALTER TABLE ONLY public.interview_sessions DROP CONSTRAINT interview_sessions_pkey;
ALTER TABLE ONLY public.interview_answers DROP CONSTRAINT interview_answers_pkey;
ALTER TABLE ONLY public.email_verifications DROP CONSTRAINT email_verifications_pkey;
ALTER TABLE ONLY public.email_verifications DROP CONSTRAINT email_verifications_email_key;
ALTER TABLE public.session_questions ALTER COLUMN id DROP DEFAULT;
ALTER TABLE public.interview_answers ALTER COLUMN id DROP DEFAULT;
DROP TABLE public.users;
DROP TABLE public.user_api_keys;
DROP TABLE public.trials;
DROP SEQUENCE public.session_questions_id_seq;
DROP TABLE public.session_questions;
DROP TABLE public.interview_sessions;
DROP SEQUENCE public.interview_answers_id_seq;
DROP TABLE public.interview_answers;
DROP TABLE public.email_verifications;
SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: email_verifications; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.email_verifications (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    password_hash text NOT NULL,
    code_hash text NOT NULL,
    attempts integer DEFAULT 0 NOT NULL,
    verified boolean DEFAULT false NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: interview_answers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.interview_answers (
    id bigint NOT NULL,
    session_id text,
    question_id integer NOT NULL,
    question_text text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    transcript text DEFAULT ''::text NOT NULL,
    score integer DEFAULT 0 NOT NULL,
    star text DEFAULT ''::text NOT NULL,
    summary text DEFAULT ''::text NOT NULL,
    filler_words jsonb DEFAULT '{}'::jsonb NOT NULL,
    answered_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: interview_answers_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.interview_answers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: interview_answers_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.interview_answers_id_seq OWNED BY public.interview_answers.id;


--
-- Name: interview_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.interview_sessions (
    id text NOT NULL,
    user_id uuid,
    job_description text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    job_title text DEFAULT ''::text NOT NULL
);


--
-- Name: session_questions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.session_questions (
    id bigint NOT NULL,
    session_id text,
    question_idx integer NOT NULL,
    question_text text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    skill text DEFAULT ''::text NOT NULL
);


--
-- Name: session_questions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.session_questions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: session_questions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.session_questions_id_seq OWNED BY public.session_questions.id;


--
-- Name: trials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.trials (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    ip_address text NOT NULL,
    tries_remaining integer DEFAULT 3 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: user_api_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_api_keys (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    user_id uuid NOT NULL,
    provider text NOT NULL,
    key_hint text DEFAULT ''::text NOT NULL,
    encrypted_key text NOT NULL,
    is_active boolean DEFAULT false NOT NULL,
    status text DEFAULT 'untested'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    email text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    password_hash text,
    provider text DEFAULT 'email'::text NOT NULL,
    provider_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    plan text DEFAULT 'free'::text NOT NULL,
    free_sessions_used integer DEFAULT 0 NOT NULL,
    role text DEFAULT 'user'::text NOT NULL,
    reset_token text,
    reset_token_expires_at timestamp with time zone
);


--
-- Name: interview_answers id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_answers ALTER COLUMN id SET DEFAULT nextval('public.interview_answers_id_seq'::regclass);


--
-- Name: session_questions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_questions ALTER COLUMN id SET DEFAULT nextval('public.session_questions_id_seq'::regclass);


--
-- Data for Name: email_verifications; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.email_verifications (id, email, name, password_hash, code_hash, attempts, verified, expires_at, created_at) FROM stdin;
\.


--
-- Data for Name: interview_answers; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.interview_answers (id, session_id, question_id, question_text, category, transcript, score, star, summary, filler_words, answered_at) FROM stdin;
\.


--
-- Data for Name: interview_sessions; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.interview_sessions (id, user_id, job_description, created_at, job_title) FROM stdin;
b580f9c3b9924c04d1deb34f05ce745c	b3aad908-0383-4f43-8c51-f2937095e129	We need a React developer with TypeScript, REST APIs, Jest testing, and 3+ years experience building scalable SPAs.	2026-04-22 12:14:46.926012+02	React Developer
705c882ef21d817b76f782d98103ea19	b3aad908-0383-4f43-8c51-f2937095e129	We need a React developer with TypeScript, REST APIs, Jest testing, and 3+ years experience building scalable SPAs.	2026-04-22 12:15:17.258371+02	React Developer
1432e1d6109cebf25d713f5f3db6deb8	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:15:57.722542+02	Backend Engineer
a15b4d8cd0972fe1f1265471bb34d8ad	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:17:45.649076+02	Backend Engineer
a3ac6496a043d453852c31b55358c954	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:19:32.253196+02	Backend Engineer
d9e9735594423a8dabe7a97ac1a7296e	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:20:20.755614+02	Backend Engineer
a03c57d70309b08eecd105b1c0ae1069	b3aad908-0383-4f43-8c51-f2937095e129	Go developer with PostgreSQL, REST APIs, Docker, microservices, 3+ years experience.	2026-04-22 12:21:15.20716+02	Backend Engineer
0806a9a023fc6538955fbe424d179bca	6b7963d5-154a-4993-8d52-eefbc496e2c8	We're looking for a Frontend Engineer to build fast, accessible, and polished user interfaces.\n\nResponsibilities:\n- Build and maintain React components and pages\n- Collaborate with designers to implement pixel-perfect UIs\n- Optimize for performance and cross-browser compatibility\n- Write clean, testable JavaScript/TypeScript\n\nRequired skills:\n- 3+ years with React and modern JS (ES6+)\n- Strong CSS and responsive design skills\n- Experience with REST APIs and state management (Redux, Zustand)\n- Familiarity with testing (Jest, React Testing Library)	2026-08-10 20:23:49.673864+02	Frontend Engineer
0e87b0aea66ffc13b7eb5df23d7e1789	6b7963d5-154a-4993-8d52-eefbc496e2c8	We're looking for a Frontend Engineer to build fast, accessible, and polished user interfaces.\n\nResponsibilities:\n- Build and maintain React components and pages\n- Collaborate with designers to implement pixel-perfect UIs\n- Optimize for performance and cross-browser compatibility\n- Write clean, testable JavaScript/TypeScript\n\nRequired skills:\n- 3+ years with React and modern JS (ES6+)\n- Strong CSS and responsive design skills\n- Experience with REST APIs and state management (Redux, Zustand)\n- Familiarity with testing (Jest, React Testing Library)	2026-08-10 21:42:05.82026+02	Frontend Engineer
d6f7202c0dd08effa3c19c706e7f6683	6b7963d5-154a-4993-8d52-eefbc496e2c8	We need a Fullstack Developer comfortable owning features end-to-end, from REST API design in Go to building the React frontend.\n\nResponsibilities:\n- Design and build REST APIs in Go\n- Build React UIs that consume those APIs\n- Write SQL queries and manage PostgreSQL schemas\n- Deploy and monitor services on cloud infrastructure\n\nRequired skills:\n- 2+ years with Go (Gin or Echo preferred)\n- Solid React and TypeScript skills\n- PostgreSQL and basic DevOps (Docker, CI/CD)	2026-08-10 22:05:28.826598+02	Fullstack Go Developer
014d45d339ab43365b247e4d559e68a7	6b7963d5-154a-4993-8d52-eefbc496e2c8	We're hiring a Java Backend Developer to build and scale microservices powering our core product.\n\nResponsibilities:\n- Develop microservices using Spring Boot\n- Design and optimise relational database schemas\n- Write unit and integration tests\n- Participate in code reviews and architecture discussions\n\nRequired skills:\n- 3+ years with Java and Spring Boot\n- Experience with JPA/Hibernate and PostgreSQL or MySQL\n- Familiarity with message queues (Kafka or RabbitMQ)\n- REST API design best practices	2026-08-10 22:26:33.305716+02	Java Backend Developer
5f33480f7d788b43db628baca3dc4db4	6b7963d5-154a-4993-8d52-eefbc496e2c8	We're hiring a Java Backend Developer to build and scale microservices powering our core product.\n\nResponsibilities:\n- Develop microservices using Spring Boot\n- Design and optimise relational database schemas\n- Write unit and integration tests\n- Participate in code reviews and architecture discussions\n\nRequired skills:\n- 3+ years with Java and Spring Boot\n- Experience with JPA/Hibernate and PostgreSQL or MySQL\n- Familiarity with message queues (Kafka or RabbitMQ)\n- REST API design best practices	2026-08-10 22:37:41.952375+02	Java Backend Developer
\.


--
-- Data for Name: session_questions; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.session_questions (id, session_id, question_idx, question_text, category, skill) FROM stdin;
83	0806a9a023fc6538955fbe424d179bca	0	Can you describe a time when a designer provided a mock-up that was difficult to implement or would impact app performance? How did you navigate that conversation and find a solution?	Behavioral	Cross-functional Collaboration
84	0806a9a023fc6538955fbe424d179bca	1	How do you approach diagnosing and fixing performance bottlenecks (such as unnecessary re-renders or large bundle sizes) in a React application?	Technical	Performance Optimization
85	0806a9a023fc6538955fbe424d179bca	2	When building a complex feature that fetches data from a REST API, how do you decide between using React local state, React Context, or a global state store like Redux or Zustand?	Technical	State Management & Architecture
86	0806a9a023fc6538955fbe424d179bca	3	Tell me about a challenging cross-browser compatibility or responsive design issue you encountered recently. How did you debug and resolve it?	Behavioral	Problem Solving
87	0806a9a023fc6538955fbe424d179bca	4	What is your strategy for writing maintainable unit and integration tests with React Testing Library and Jest without over-testing implementation details?	Technical	Testing & Code Quality
88	0e87b0aea66ffc13b7eb5df23d7e1789	0	Can you describe a time when a designer provided a complex design that was challenging to implement? How did you collaborate with them to achieve a pixel-perfect result without compromising on performance or accessibility?	Behavioral	Cross-functional Collaboration
89	0e87b0aea66ffc13b7eb5df23d7e1789	1	How do you decide between managing state locally, via React Context, or using a global state library like Redux or Zustand in a complex web application?	Technical	State Management
13	705c882ef21d817b76f782d98103ea19	0	How do you leverage TypeScript's advanced features, such as Generics or Discriminated Unions, to ensure type safety when handling diverse data structures from a REST API?	Technical	TypeScript
14	705c882ef21d817b76f782d98103ea19	1	Describe your strategy for writing maintainable tests with Jest. How do you decide between unit testing individual hooks and integration testing full component trees?	Technical	Jest testing
15	705c882ef21d817b76f782d98103ea19	2	Tell me about a specific time you identified a performance bottleneck in a large-scale React SPA. What steps did you take to diagnose the issue and how did you resolve it?	Behavioral	Problem Solving
16	705c882ef21d817b76f782d98103ea19	3	When designing a scalable SPA architecture, how do you organize your folder structure and state management to ensure the project remains maintainable as it grows over 3+ years?	Technical	System Design
17	705c882ef21d817b76f782d98103ea19	4	Describe a situation where you had to advocate for a specific technical approach (like a library choice or architectural pattern) to your team. How did you communicate the benefits and handle any pushback?	Behavioral	Communication
18	1432e1d6109cebf25d713f5f3db6deb8	0	Explain how you manage concurrency in Go using goroutines and channels. Can you describe a scenario where you had to debug a race condition or a deadlock in a production microservice?	Technical	Go Concurrency
19	1432e1d6109cebf25d713f5f3db6deb8	1	Describe your process for optimizing a slow PostgreSQL query. How do you utilize tools like EXPLAIN ANALYZE, and what strategies do you use for indexing or schema refactoring in a high-traffic environment?	Technical	PostgreSQL Optimization
20	1432e1d6109cebf25d713f5f3db6deb8	2	When designing a REST API for a microservice, how do you handle versioning, error handling, and ensuring the service remains scalable and containerized with Docker?	Technical	System Design
21	1432e1d6109cebf25d713f5f3db6deb8	3	Tell me about a time you had to lead a technical initiative or mentor a junior developer. How did you ensure the project met its deadlines while maintaining high code quality?	Behavioral	Leadership
22	1432e1d6109cebf25d713f5f3db6deb8	4	Describe a situation where you disagreed with a peer's architectural choice for a backend service. How did you communicate your perspective and reach a resolution that benefited the project?	Behavioral	Communication
23	a03c57d70309b08eecd105b1c0ae1069	0	Explain how you would implement a worker pool pattern in Go using goroutines and channels. How do you ensure that the application handles graceful shutdowns without losing in-flight data?	Technical	Go Concurrency
24	a03c57d70309b08eecd105b1c0ae1069	1	Describe your process for identifying and optimizing a slow-performing PostgreSQL query in a production environment. When would you choose a GIN index over a B-tree index?	Technical	PostgreSQL
90	0e87b0aea66ffc13b7eb5df23d7e1789	2	Walk me through your process for diagnosing and fixing performance bottlenecks in a React application, such as excessive re-renders or slow initial render times.	Technical	Performance Optimization
91	0e87b0aea66ffc13b7eb5df23d7e1789	3	How do you approach writing testable JavaScript/TypeScript components, and what best practices do you follow when writing unit and integration tests with Jest and React Testing Library?	Technical	Testing & Quality Assurance
92	0e87b0aea66ffc13b7eb5df23d7e1789	4	Tell me about a time you encountered a difficult cross-browser layout or CSS specificity bug late in a development cycle. How did you troubleshoot and resolve it?	Behavioral	Problem Solving
25	a03c57d70309b08eecd105b1c0ae1069	2	In a microservices architecture, how do you manage data consistency across services when a REST API call triggers updates in multiple databases? Explain your experience with patterns like Saga or Two-Phase Commit.	Technical	System Design
26	a03c57d70309b08eecd105b1c0ae1069	3	Tell me about a time you encountered a significant performance bottleneck in a Dockerized Go application. How did you profile the service, and what steps did you take to resolve the issue?	Behavioral	Problem Solving
27	a03c57d70309b08eecd105b1c0ae1069	4	Describe a situation where you had to advocate for a specific backend architectural change (e.g., moving to a new library or refactoring a service). How did you communicate the technical debt and benefits to your team?	Behavioral	Communication
98	d6f7202c0dd08effa3c19c706e7f6683	0	Can you walk through an end-to-end feature you built from scratch? How did you design the REST API in Go, structure the PostgreSQL schema, and ensure type safety when consuming the data in React with TypeScript?	Behavioral	End-to-End Feature Ownership
99	d6f7202c0dd08effa3c19c706e7f6683	1	How do you structure a Go REST API using a framework like Gin or Echo to handle middleware, request validation, and idiomatic error handling consistently?	Technical	Go REST API Design
100	d6f7202c0dd08effa3c19c706e7f6683	2	How do you manage API state, asynchronous data fetching, and type synchronization between backend DTOs and frontend interfaces in a React + TypeScript application?	Technical	React & TypeScript
101	d6f7202c0dd08effa3c19c706e7f6683	3	How would you identify and optimize a slow API endpoint caused by an inefficient PostgreSQL query, and what strategy do you use for managing database schema migrations safely in production?	Technical	PostgreSQL & Performance Tuning
102	d6f7202c0dd08effa3c19c706e7f6683	4	Tell me about a time a containerized application or CI/CD deployment pipeline failed in production or staging. How did you monitor, diagnose, and resolve the issue?	Behavioral	DevOps & Problem Solving
103	014d45d339ab43365b247e4d559e68a7	0	How do you identify and resolve the N+1 query problem when using JPA/Hibernate in a Spring Boot application, and what additional strategies do you use to optimize database performance in PostgreSQL or MySQL?	Technical	Database Optimization
104	014d45d339ab43365b247e4d559e68a7	1	Can you walk through how you would design an asynchronous, event-driven communication flow between microservices using Kafka or RabbitMQ, specifically addressing message delivery guarantees and failure handling?	Technical	System Design
105	014d45d339ab43365b247e4d559e68a7	2	Describe a scenario where you disagreed with a teammate's architectural proposal or code implementation during a code review. How did you navigate the discussion and arrive at a consensus?	Behavioral	Communication & Collaboration
106	014d45d339ab43365b247e4d559e68a7	3	What best practices do you follow when designing RESTful APIs for backward compatibility, and how do you structure your unit and integration tests (e.g., using Mockito or Testcontainers) in Spring Boot to ensure high quality?	Technical	REST API & Testing
107	014d45d339ab43365b247e4d559e68a7	4	Tell me about a time when a backend microservice you maintained experienced a performance bottleneck or outage in production. How did you troubleshoot, diagnose, and permanently resolve the issue?	Behavioral	Problem Solving
108	5f33480f7d788b43db628baca3dc4db4	0	How do you approach designing resilient RESTful microservices in Spring Boot, particularly regarding standardized error handling, API versioning, and request validation?	Technical	REST API Design & Spring Boot
109	5f33480f7d788b43db628baca3dc4db4	1	Can you walk me through how you identify and resolve database performance issues in JPA/Hibernate, such as the N+1 query problem, when working with PostgreSQL or MySQL?	Technical	Database Optimization & JPA
110	5f33480f7d788b43db628baca3dc4db4	2	How do you ensure message delivery reliability and handle idempotent event processing when integrating microservices with Kafka or RabbitMQ?	Technical	Message Queues & Architecture
111	5f33480f7d788b43db628baca3dc4db4	3	Describe a situation where you had a strong disagreement with a teammate during an architectural discussion or code review. How did you navigate the conversation and reach a consensus?	Behavioral	Code Reviews & Collaboration
112	5f33480f7d788b43db628baca3dc4db4	4	Tell me about a time a core microservice encountered a performance bottleneck or failure in production. How did you diagnose the issue, and what unit/integration tests did you add to prevent regressions?	Behavioral	Problem Solving & Quality Assurance
\.


--
-- Data for Name: trials; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.trials (id, ip_address, tries_remaining, created_at) FROM stdin;
\.


--
-- Data for Name: user_api_keys; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.user_api_keys (id, user_id, provider, key_hint, encrypted_key, is_active, status, created_at, updated_at) FROM stdin;
\.


--
-- Data for Name: users; Type: TABLE DATA; Schema: public; Owner: -
--

COPY public.users (id, email, name, password_hash, provider, provider_id, created_at, updated_at, plan, free_sessions_used, role, reset_token, reset_token_expires_at) FROM stdin;
b3aad908-0383-4f43-8c51-f2937095e129	test@interviewdojo.com	Test User	$2a$10$ZpeLg9BhV3hPEngFKGwXZ.hg8E4ry13Ih.H9NYZhMstyN2NUVh.XS	email	\N	2026-04-21 13:08:43.739067+02	2026-04-21 13:08:43.739067+02	free	0	user	\N	\N
c56e975d-28a4-4ce7-96fd-b4478adf3b33	quota@test.com	Quota Test	$2a$10$npXyAlF9i2BiWn2KzwF8Ke8S4XvM40NiRjP8AFZooUgPFCtDLSUta	email	\N	2026-04-22 14:03:08.554669+02	2026-04-22 14:03:08.554669+02	free	0	user	\N	\N
fa9e13e8-801c-401c-a2fd-987b8f6c04f3	quota2@test.com	Quota Test	$2a$10$/GAZWzA66UkBDlDy9zWH../F1iZPXcQi4k/osWEEUAO8OLiKyC9Ue	email	\N	2026-04-22 14:03:35.958953+02	2026-04-22 14:03:35.958953+02	free	0	user	\N	\N
7ac59d1c-7ea7-4237-8a0b-96fd00803f76	quota3@test.com	Quota Test	$2a$10$A/TndRogSEWwgn4PWKDd8u8feTTDDC0UbeGqRaTH6yuwTw8rfror6	email	\N	2026-04-22 14:04:19.518646+02	2026-04-22 14:04:19.518646+02	free	0	user	\N	\N
c4e36712-6c86-4dfc-8b4d-c82a647f1ef2	test2@test.com	Test	$2a$10$ZzQAIuYXF2g799mW.DeTgeTKn5jq/WzDWkioOzK7tAu2JzKtu9Wmu	email	\N	2026-04-22 14:19:18.051464+02	2026-04-22 14:19:18.051464+02	free	0	user	\N	\N
da3bf9f6-7706-4ed2-a35f-df6c56512e9f	newuser@test.com	New User	$2a$10$CskUjYTt4c3/zNkPObd3gOUB8DijZuxpC1WgBBgwyWrsEmBEYLiCq	email	\N	2026-04-22 14:35:55.300853+02	2026-04-22 14:35:55.300853+02	free	0	user	\N	\N
6b7963d5-154a-4993-8d52-eefbc496e2c8	babongilenkosimphile101@gmail.com	Babongile Nkosimphile	$2a$10$D3A8o2eoFpnoE0yT8XvhB.GxB2gvGKYlFlOZTrE1le2pN0UtZV26y	email	\N	2026-04-21 13:10:12.07191+02	2026-04-29 14:33:23.651747+02	enterprise	0	admin	\N	\N
a6baa579-0ca2-4168-a1fc-fbb809b7bb27	nkosi_10@outlook.com	Mbali 	$2a$10$h8k4lO0sumysT4eQZYwNC.y.8f56MK4QpWCcutiE20N2Zv3BZmKPa	email	\N	2026-04-22 14:43:09.935736+02	2026-08-10 17:22:55.677722+02	free	0	user	569f86d2f4836612ea9110e09a1d6dc8107f50eacc8d66abd2f8b18f5b57e893	2026-08-10 17:52:55.677499+02
\.


--
-- Name: interview_answers_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.interview_answers_id_seq', 1, false);


--
-- Name: session_questions_id_seq; Type: SEQUENCE SET; Schema: public; Owner: -
--

SELECT pg_catalog.setval('public.session_questions_id_seq', 112, true);


--
-- Name: email_verifications email_verifications_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_verifications
    ADD CONSTRAINT email_verifications_email_key UNIQUE (email);


--
-- Name: email_verifications email_verifications_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.email_verifications
    ADD CONSTRAINT email_verifications_pkey PRIMARY KEY (id);


--
-- Name: interview_answers interview_answers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_answers
    ADD CONSTRAINT interview_answers_pkey PRIMARY KEY (id);


--
-- Name: interview_sessions interview_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_sessions
    ADD CONSTRAINT interview_sessions_pkey PRIMARY KEY (id);


--
-- Name: session_questions session_questions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_questions
    ADD CONSTRAINT session_questions_pkey PRIMARY KEY (id);


--
-- Name: trials trials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.trials
    ADD CONSTRAINT trials_pkey PRIMARY KEY (id);


--
-- Name: user_api_keys user_api_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_api_keys
    ADD CONSTRAINT user_api_keys_pkey PRIMARY KEY (id);


--
-- Name: user_api_keys user_api_keys_user_id_provider_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_api_keys
    ADD CONSTRAINT user_api_keys_user_id_provider_key UNIQUE (user_id, provider);


--
-- Name: users users_email_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_email_key UNIQUE (email);


--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);


--
-- Name: idx_answers_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_answers_session ON public.interview_answers USING btree (session_id);


--
-- Name: idx_apikeys_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_apikeys_user ON public.user_api_keys USING btree (user_id);


--
-- Name: idx_email_verif_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_email_verif_email ON public.email_verifications USING btree (email);


--
-- Name: idx_questions_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_questions_session ON public.session_questions USING btree (session_id);


--
-- Name: idx_sessions_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sessions_user ON public.interview_sessions USING btree (user_id);


--
-- Name: idx_trials_ip; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_trials_ip ON public.trials USING btree (ip_address);


--
-- Name: idx_users_email; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_users_email ON public.users USING btree (email);


--
-- Name: interview_answers interview_answers_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.interview_answers
    ADD CONSTRAINT interview_answers_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.interview_sessions(id) ON DELETE CASCADE;


--
-- Name: session_questions session_questions_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.session_questions
    ADD CONSTRAINT session_questions_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.interview_sessions(id) ON DELETE CASCADE;


--
-- Name: user_api_keys user_api_keys_user_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_api_keys
    ADD CONSTRAINT user_api_keys_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict b8tc2XSD0qFuHRmg6HcVYSBvdezPZQ33vwKX5glxWhnZMjbqbGC7YExWOeXMoMw

